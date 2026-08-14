package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// stubClient serves a fixed GraphQL response for any query, and reports whether
// upstream was called at all. The genqlient client is real — only the transport
// is stubbed — so response decoding is exercised, which is where a schema or
// field-name mismatch would actually bite.
func stubClient(t *testing.T, status int, body string) (*graphql.Client, *bool) {
	t.Helper()

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := graphql.NewClient(srv.URL, srv.Client())
	return &c, &called
}

// Bad UUIDs are rejected locally: upstream answers "area Invalid UUID.", which
// does not tell a model what to fix.
func TestGetAreaDetailsValidatesUUID(t *testing.T) {
	valid := "8f267065-fc1a-59ce-bcf1-6e9335548363"
	body := `{"data":{"area":{"uuid":"` + valid + `","areaName":"Stawamus Chief","totalClimbs":369,"metadata":{"lat":49.68,"lng":-123.14}}}}`

	// The dashless 32-hex form parses fine but the API rejects it, so it is
	// caught by the hyphen check rather than by uuid.Parse.
	bad := []struct {
		areaID string
		want   string
	}{
		{"", "invalid UUID length: 0"},
		{"   ", "invalid UUID length: 3"},
		{"not-a-uuid", "invalid UUID length: 10"},
		{"8f267065fc1a59cebcf16e9335548363", "uuid: missing required hyphens"},
		{valid + "-extra", "invalid UUID length: " + strconv.Itoa(len(valid)+6)},
	}
	for _, tt := range bad {
		gql, called := stubClient(t, 200, body)

		h := HandleGetAreaDetails(gql)
		_, _, err := h(context.Background(), nil, GetAreaDetailsArgs{AreaID: tt.areaID})
		if err == nil {
			t.Errorf("AreaID %q was accepted, want an error", tt.areaID)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("AreaID %q: error %q does not explain the problem (want %q)", tt.areaID, err, tt.want)
		}
		if *called {
			t.Errorf("AreaID %q: upstream was called despite invalid input", tt.areaID)
		}
	}

	gql, _ := stubClient(t, 200, body)
	h := HandleGetAreaDetails(gql)
	_, out, err := h(context.Background(), nil, GetAreaDetailsArgs{AreaID: valid})
	if err != nil {
		t.Fatalf("valid uuid rejected: %v", err)
	}
	if out.Area.AreaName != "Stawamus Chief" {
		t.Errorf("AreaName = %q, want %q", out.Area.AreaName, "Stawamus Chief")
	}
}
