package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
)

// routingStub answers CragsNear with one body and GetAreaDetails with whatever
// detailFor returns for the requested uuid, because crags_near calls both. A
// single fixed body cannot exercise the fan-out.
//
// Returning an error from detailFor produces a GraphQL error for that uuid,
// which is how a partial upstream failure is simulated.
func routingStub(t *testing.T, nearBody string, detailFor func(uuid string) (body string, fail bool)) (graphql.Client, *atomic.Bool) {
	t.Helper()

	// atomic because the fan-out issues several requests at once, so the
	// handler runs on concurrent goroutines.
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		body, _ := io.ReadAll(r.Body)
		req := string(body)

		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req, "GetAreaDetails") {
			uuid := uuidFromRequest(req)
			body, fail := detailFor(uuid)
			if fail {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("error code: 502"))
				return
			}
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(nearBody))
	}))
	t.Cleanup(srv.Close)

	return graphql.NewClient(srv.URL, srv.Client()), &called
}

// uuidFromRequest pulls the uuid variable out of the raw request body. Crude,
// but it avoids decoding the genqlient envelope in a test.
func uuidFromRequest(req string) string {
	const key = `"uuid":"`
	i := strings.Index(req, key)
	if i < 0 {
		return ""
	}
	rest := req[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func nearBody(uuids ...string) string {
	var crags []string
	for i, u := range uuids {
		crags = append(crags, `{"uuid":"`+u+`","areaName":"Crag `+u+`","totalClimbs":0,"pathTokens":["Canada"],`+
			`"metadata":{"lat":`+[]string{"49.70", "49.71", "49.72"}[i%3]+`,"lng":-123.15,"leaf":true,"isBoulder":false}}`)
	}
	return `{"data":{"cragsNear":[{"count":` + itoa(len(uuids)) + `,"crags":[` + strings.Join(crags, ",") + `]}]}}`
}

func detailBody(uuid string, climbs int) string {
	var cl []string
	for i := 0; i < climbs; i++ {
		cl = append(cl, `{"uuid":"c`+itoa(i)+`","name":"Route `+itoa(i)+`","fa":"","length":-1,"safety":"UNSPECIFIED",`+
			`"type":{"sport":true},"grades":{"yds":"5.10a"}}`)
	}
	return `{"data":{"area":{"uuid":"` + uuid + `","areaName":"Crag ` + uuid + `","totalClimbs":0,` +
		`"metadata":{"lat":49.70,"lng":-123.15},"climbs":[` + strings.Join(cl, ",") + `],"children":[]}}}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Invalid input must be rejected before the network call, so a bad argument
// costs nothing upstream (FR-18).
func TestCragsNearValidatesBeforeCallingUpstream(t *testing.T) {
	tests := []struct {
		name string
		args CragsNearArgs
		want string
	}{
		{"neither place nor lnglat", CragsNearArgs{}, "pass a place name"},
		{"both", CragsNearArgs{Place: "Squamish", LngLat: []float64{-123.1, 49.7}}, "not both"},
		{"wrong length", CragsNearArgs{LngLat: []float64{-123.1}}, "exactly 2 elements"},
		{"transposed", CragsNearArgs{LngLat: []float64{49.7, -123.1}}, "latitude out of range"},
		{"unknown place", CragsNearArgs{Place: "Nowhere At All"}, "no coordinates known"},
		{"radius too large", CragsNearArgs{Place: "Squamish", MaxDistanceKm: ptr(9000.0)}, "at most 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql, called := routingStub(t, nearBody("a"), func(string) (string, bool) { return detailBody("a", 1), false })

			_, _, err := HandleCragsNear(gql, geo.NewGazetteer())(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tt.want)
			}
			if called.Load() {
				t.Error("upstream was called despite invalid input")
			}
		})
	}
}

// One crag failing its detail call must not erase the rest: upstream returns
// intermittent 502s and a partial answer is still useful.
func TestCragsNearSurvivesPartialFanOutFailure(t *testing.T) {
	gql, _ := routingStub(t, nearBody("a", "b", "c"), func(uuid string) (string, bool) {
		if uuid == "b" {
			return "", true
		}
		return detailBody(uuid, 5), false
	})

	_, out, err := HandleCragsNear(gql, geo.NewGazetteer())(context.Background(), nil,
		CragsNearArgs{Place: "Squamish"})
	if err != nil {
		t.Fatalf("one failed crag should not fail the call: %v", err)
	}
	if len(out.Crags) != 2 {
		t.Fatalf("expected the two surviving crags, got %d: %+v", len(out.Crags), out.Crags)
	}
	for _, c := range out.Crags {
		if c.UUID == "b" {
			t.Error("the failed crag should have been dropped")
		}
		if c.ClimbCount != 5 {
			t.Errorf("%s ClimbCount = %d, want 5", c.UUID, c.ClimbCount)
		}
	}
	if out.Origin.Source != "gazetteer" {
		t.Errorf("Origin.Source = %q, want gazetteer", out.Origin.Source)
	}
}

// Every detail call failing is an outage, and an empty list would read as
// "no crags here".
func TestCragsNearTotalFanOutFailureIsAnError(t *testing.T) {
	gql, _ := routingStub(t, nearBody("a", "b"), func(string) (string, bool) { return "", true })

	_, _, err := HandleCragsNear(gql, geo.NewGazetteer())(context.Background(), nil,
		CragsNearArgs{Place: "Squamish"})
	if err == nil {
		t.Fatal("expected an error when every detail call fails")
	}
}

func ptr[T any](v T) *T { return &v }

// The schema told the model count "may exceed the crags array", and the code
// set it after truncating, so it never could. A model cannot tell twenty crags
// nearby from five hundred, and that was the number meant to tell it.
func TestCragsNearCountsBeforeTheCap(t *testing.T) {
	t.Run("count exceeds the cap", func(t *testing.T) {
		var uuids []string
		for i := 0; i < MaxCrags+5; i++ {
			uuids = append(uuids, "c"+itoa(i))
		}
		gql, _ := routingStub(t, nearBody(uuids...), func(uuid string) (string, bool) {
			return detailBody(uuid, 3), false
		})

		_, out, err := HandleCragsNear(gql, geo.NewGazetteer())(context.Background(), nil,
			CragsNearArgs{Place: "Squamish"})
		if err != nil {
			t.Fatalf("crags_near: %v", err)
		}
		if out.Count != len(uuids) {
			t.Errorf("Count = %d, want %d — the radius found that many", out.Count, len(uuids))
		}
		if out.Returned != MaxCrags || len(out.Crags) != MaxCrags {
			t.Errorf("Returned = %d with %d crags, want %d of each", out.Returned, len(out.Crags), MaxCrags)
		}
		if out.Count <= out.Returned {
			t.Error("Count must be able to exceed Returned; that gap is the whole signal")
		}
	})

	t.Run("count includes crags holding nothing", func(t *testing.T) {
		// Documented as an upper bound for exactly this reason: the count is
		// taken before the climb filter, so it counts crags a caller will never
		// see. An imprecise number honestly described beats a precise wrong one.
		gql, _ := routingStub(t, nearBody("a", "b", "c"), func(uuid string) (string, bool) {
			if uuid == "b" {
				return detailBody(uuid, 0), false
			}
			return detailBody(uuid, 4), false
		})

		_, out, err := HandleCragsNear(gql, geo.NewGazetteer())(context.Background(), nil,
			CragsNearArgs{Place: "Squamish"})
		if err != nil {
			t.Fatalf("crags_near: %v", err)
		}
		if out.Count != 3 {
			t.Errorf("Count = %d, want 3", out.Count)
		}
		if out.Returned != 2 {
			t.Errorf("Returned = %d, want 2 — the empty crag is dropped from the array", out.Returned)
		}
	})
}
