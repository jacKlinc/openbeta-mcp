package openbeta

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

// New must apply its options to the fields callers actually read back, and must
// default the rest.
//
// This is a regression guard, not a triviality: mcpserver.New builds its
// genqlient client from Endpoint() and HTTPClient(). While it read
// DefaultEndpoint directly instead, WithEndpoint was silently ineffective and
// every test that thought it was stubbing upstream queried the live API.
func TestOptionsReachTheFieldsCallersRead(t *testing.T) {
	if got := New().Endpoint(); got != DefaultEndpoint {
		t.Errorf("Endpoint() = %q, want the public API %q", got, DefaultEndpoint)
	}
	if got := New().HTTPClient().Timeout; got != defaultTimeout {
		t.Errorf("HTTPClient().Timeout = %v, want %v", got, defaultTimeout)
	}

	const endpoint = "http://127.0.0.1:1/graphql"
	if got := New(WithEndpoint(endpoint)).Endpoint(); got != endpoint {
		t.Errorf("Endpoint() = %q, want %q", got, endpoint)
	}

	custom := &http.Client{Timeout: 7 * time.Second}
	if got := New(WithHTTPClient(custom)).HTTPClient(); got != custom {
		t.Errorf("HTTPClient() = %p, want the injected client %p", got, custom)
	}
}
