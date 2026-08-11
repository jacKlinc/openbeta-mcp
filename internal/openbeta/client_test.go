package openbeta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient serves a fixed response body for any query.
func newTestClient(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected JSON content type, got %q", ct)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(WithEndpoint(srv.URL))
}

// TODO: fix me
// func TestValidateAreaID(t *testing.T) {
// 	valid := "8f267065-fc1a-59ce-bcf1-6e9335548363"
// 	if err := ValidateAreaID(valid); err != nil {
// 		t.Fatalf("valid uuid rejected: %v", err)
// 	}
// 	for _, bad := range []string{"", "   ", "not-a-uuid", "8f267065fc1a59cebcf16e9335548363", valid + "-extra"} {
// 		if err := ValidateAreaID(bad); err == nil {
// 			t.Errorf("ValidateAreaID(%q) = nil, want error", bad)
// 		}
// 	}
// }

// Validation must happen before the network call, so a bad argument costs
// nothing upstream (FR-18).
func TestValidationSkipsUpstreamCall(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()
	c := New(WithEndpoint(srv.URL))

	if _, err := c.CragsWithin(context.Background(), BBox{0, 0, -10, 10}, 11); err == nil {
		t.Error("expected error for reversed bbox")
	}
	// TODO: fix me
	// if _, err := c.GetArea(context.Background(), "nope"); err == nil {
	// 	t.Error("expected error for bad uuid")
	// }
	if called {
		t.Error("upstream was called despite invalid input")
	}
}

// A GraphQL error arrives with HTTP 200 and must not be reported as an empty
// result set (FR-16).
func TestGraphQLErrorSurfaces(t *testing.T) {
	c := newTestClient(t, 200, `{"errors":[{"message":"Cannot query field \"nope\""}]}`)

	_, err := c.CragsWithin(context.Background(), BBox{-123.2, 49.6, -122.9, 49.8}, 11)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "Cannot query field") {
		t.Errorf("error should carry the upstream message, got %q", err)
	}
}

// The API returns bare non-JSON error pages (an observed 502 on malformed
// queries); the error should name the status, not a JSON parse failure (FR-17).
func TestNonJSONErrorPageSurfaces(t *testing.T) {
	c := newTestClient(t, 502, "error code: 502")

	_, err := c.CragsWithin(context.Background(), BBox{-123.2, 49.6, -122.9, 49.8}, 11)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should mention the status, got %q", err)
	}
}

// TODO: fix me
// func TestUnreachableUpstreamSurfaces(t *testing.T) {
// 	// Port 1 is reserved and will refuse the connection.
// 	c := New(WithEndpoint("http://127.0.0.1:1/graphql"))
// 	if _, err := c.GetArea(context.Background(), "8f267065-fc1a-59ce-bcf1-6e9335548363"); err == nil {
// 		t.Fatal("expected transport error, got nil")
// 	}
// }

// An empty box is a valid answer, and must marshal as [] rather than null so a
// client cannot mistake it for a missing field (FR-11).
func TestEmptyResultIsNotAnError(t *testing.T) {
	c := newTestClient(t, 200, `{"data":{"cragsWithin":[]}}`)

	got, err := c.CragsWithin(context.Background(), BBox{-140, -50, -139, -49}, 11)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no crags, got %d", len(got))
	}
	b, _ := json.Marshal(got)
	if string(b) != "[]" {
		t.Errorf("empty result marshalled as %s, want []", b)
	}
}

// TODO: fix me
// func TestGetAreaNotFound(t *testing.T) {
// 	c := newTestClient(t, 200, `{"data":{"area":null}}`)

// 	_, err := c.GetArea(context.Background(), "00000000-0000-0000-0000-000000000000")
// 	var notFound *ErrAreaNotFound
// 	if !errors.As(err, &notFound) {
// 		t.Fatalf("expected *ErrAreaNotFound, got %T: %v", err, err)
// 	}
// }
