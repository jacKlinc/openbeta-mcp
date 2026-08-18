package openbeta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestNewInstallsCounter guards the wiring, not the arithmetic: New once built
// its *http.Client without a Transport, so RoundTrip never ran and every
// measurement silently read zero. A counting test that constructs the transport
// by hand would still have passed.
func TestNewInstallsCounter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	for _, tc := range []struct {
		name   string
		client *Client
	}{
		{"default", New()},
		// WithHTTPClient replaces the whole client, so it is the option most
		// likely to drop the transport again.
		{"with http client", New(WithHTTPClient(&http.Client{}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			req, err := http.NewRequestWithContext(WithCounter(context.Background(), &n), http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := tc.client.HTTPClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			if got := n.Load(); got != 1 {
				t.Errorf("round trips counted = %d, want 1", got)
			}
		})
	}
}

// TestCounterAbsentFromContext covers requests made outside a tool call, which
// must not panic just because no counter was attached.
func TestCounterAbsentFromContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	resp, err := New().HTTPClient().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
