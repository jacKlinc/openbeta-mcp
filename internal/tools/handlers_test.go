package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// squamish is a valid bbox, so tests exercising upstream behaviour are not
// stopped by validation first.
var squamish = []float64{-123.2, 49.6, -122.9, 49.8}

// The handler must count climbs the way ClimbCount does, not by totalClimbs, or
// real crags are dropped and the ranking is wrong. Tantalus Wall (totalClimbs 0,
// 8 climbs) outranking a parent that reports 5 is the whole point.
func TestCragsWithinCountsAndSorts(t *testing.T) {
	body := `{"data":{"cragsWithin":[
		{"uuid":"small","areaName":"Small Parent","totalClimbs":5,"pathTokens":["Canada"],"metadata":{"lat":1,"lng":2},"climbs":[]},
		{"uuid":"tantalus","areaName":"Tantalus Wall","totalClimbs":0,"pathTokens":["Canada"],"metadata":{"lat":3,"lng":4},"climbs":[{"uuid":"a"},{"uuid":"b"},{"uuid":"c"},{"uuid":"d"},{"uuid":"e"},{"uuid":"f"},{"uuid":"g"},{"uuid":"h"}]},
		{"uuid":"empty","areaName":"Empty Area","totalClimbs":0,"pathTokens":["Canada"],"metadata":{"lat":5,"lng":6},"climbs":[]}
	]}}`
	gql, _ := stubClient(t, 200, body)

	out, err := cragsWithinResult(t, gql, CragsWithinArgs{BBox: squamish})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(out.Crags) != 2 {
		t.Fatalf("expected the empty area dropped, got %d crags: %+v", len(out.Crags), out.Crags)
	}
	if out.Crags[0].Name != "Tantalus Wall" {
		t.Errorf("sorted by totalClimbs, not climb count: got %q first", out.Crags[0].Name)
	}
	if out.Crags[0].ClimbCount != 8 {
		t.Errorf("ClimbCount = %d, want 8 (len(climbs), not totalClimbs)", out.Crags[0].ClimbCount)
	}
	if out.Crags[1].ClimbCount != 5 {
		t.Errorf("parent ClimbCount = %d, want 5 (totalClimbs fallback)", out.Crags[1].ClimbCount)
	}
	if out.Count != len(out.Crags) {
		t.Errorf("Count = %d, want %d", out.Count, len(out.Crags))
	}
}

// Invalid input must be rejected before the network call, so a bad argument
// costs nothing upstream (FR-18).
func TestCragsWithinValidatesBeforeCallingUpstream(t *testing.T) {
	tests := []struct {
		name string
		bbox []float64
		want string
	}{
		{"too short", []float64{1, 2}, "4 elements"},
		{"lng reversed", []float64{0, 0, -10, 10}, "minLng"},
		{"lat/lng transposed", []float64{49.6, -123.2, 49.8, -122.9}, "latitude out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql, called := stubClient(t, 200, `{"data":{"cragsWithin":[]}}`)

			_, err := cragsWithinResult(t, gql, CragsWithinArgs{BBox: tt.bbox})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tt.want)
			}
			if *called {
				t.Error("upstream was called despite invalid input")
			}
		})
	}
}

// An empty box is a valid answer, not an error (FR-11).
func TestCragsWithinEmptyResultIsNotAnError(t *testing.T) {
	gql, _ := stubClient(t, 200, `{"data":{"cragsWithin":[]}}`)

	out, err := cragsWithinResult(t, gql, CragsWithinArgs{BBox: squamish})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Count != 0 || len(out.Crags) != 0 {
		t.Fatalf("expected no crags, got %d: %+v", out.Count, out.Crags)
	}
}

// A GraphQL error arrives with HTTP 200 and must not be reported as an empty
// result set, which a model would read as "no crags here" (FR-16).
func TestCragsWithinGraphQLErrorSurfaces(t *testing.T) {
	gql, _ := stubClient(t, 200, `{"errors":[{"message":"Cannot query field \"nope\""}]}`)

	_, err := cragsWithinResult(t, gql, CragsWithinArgs{BBox: squamish})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "Cannot query field") {
		t.Errorf("error should carry the upstream message, got %q", err)
	}
}

// The API returns bare error pages behind Cloudflare — an observed 502, and a
// 503 whose body is an nginx HTML page. Neither should surface as a JSON parse
// failure (FR-17). See docs/retry.md.
func TestCragsWithinUpstreamErrorPageSurfaces(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"502 plain text", 502, "error code: 502", "502"},
		{"503 nginx html", 503, "<html>\r\n<head><title>503 Service Temporarily Unavailable</title></head>\r\n<body></body>\r\n</html>", "503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql, _ := stubClient(t, tt.status, tt.body)

			_, err := cragsWithinResult(t, gql, CragsWithinArgs{BBox: squamish})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			// The status is what distinguishes a retryable outage from a bad
			// query, so it must survive to the caller rather than becoming a
			// JSON parse failure.
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error should name the status %s, got %q", tt.want, err)
			}
		})
	}
}

// Bad UUIDs are rejected locally: upstream answers "area Invalid UUID.", which
// does not tell a model what to fix.
func TestGetAreaDetailsValidatesUUID(t *testing.T) {
	valid := "8f267065-fc1a-59ce-bcf1-6e9335548363"
	body := `{"data":{"area":{"uuid":"` + valid + `","areaName":"Stawamus Chief","totalClimbs":369,"metadata":{"lat":49.68,"lng":-123.14}}}}`

	for _, bad := range []string{"", "   ", "not-a-uuid", "8f267065fc1a59cebcf16e9335548363", valid + "-extra"} {
		gql, called := stubClient(t, 200, body)

		h := HandleGetAreaDetails(gql)
		_, _, err := h(context.Background(), nil, GetAreaDetailsArgs{AreaID: bad})
		if err == nil {
			t.Errorf("AreaID %q was accepted, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "not a valid UUID") {
			t.Errorf("AreaID %q: error %q does not explain the problem", bad, err)
		}
		if *called {
			t.Errorf("AreaID %q: upstream was called despite invalid input", bad)
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

// A refused connection must surface as an error rather than an empty result.
func TestUnreachableUpstreamSurfaces(t *testing.T) {
	// Port 1 is reserved and will refuse the connection.
	c := graphql.NewClient("http://127.0.0.1:1/graphql", http.DefaultClient)

	h := HandleCragsWithin(&c)
	_, _, err := h(context.Background(), nil, CragsWithinArgs{BBox: squamish})
	if err == nil {
		t.Fatal("expected a transport error, got nil")
	}
}

// cragsWithinResult calls the handler and returns just the payload, since the
// *mcp.CallToolResult is always nil on this path.
func cragsWithinResult(t *testing.T, gql *graphql.Client, args CragsWithinArgs) (CragsWithinResult, error) {
	t.Helper()
	h := HandleCragsWithin(gql)
	_, out, err := h(context.Background(), nil, args)
	return out, err
}
