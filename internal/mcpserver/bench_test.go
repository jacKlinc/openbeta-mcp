//go:build bench

// Benchmarks that measure real upstream cost, tagged out of every ordinary
// build. `go test ./...` and the CI build job never compile this file, so the
// only way to fire ~450 requests at a free, volunteer-run API is to ask for it
// by name. Precedent for the tag: tools.go.
//
// Methodology, sample sizes and the analysis script live in
// data/round-trip/README.md.
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
)

// benchCallTimeout bounds one tool call. Generous against the ~1.2s a
// twenty-crag fan-out takes, so a slow crag does not end the run.
const benchCallTimeout = 60 * time.Second

// Per-tool call budgets. The default -benchtime=1s ramps b.N through 1, 100,
// 10000 — on a fan-out tool that is over 200,000 upstream requests — so a run
// without an explicit -benchtime=Nx must fail rather than fire.
const (
	maxAreaIters   = 50
	maxCragsIters  = 20
	maxClimbsIters = 20
)

// areaQueries pins the get_area_details inputs. Stawamus Chief is the anchor
// used by internal/openbeta/live_test.go; the four children span the response
// shapes the tool returns — a large sub-area list, two mid-sized climb lists and
// a small one — so a one-round-trip tool still varies in payload.
//
// Rediscover with:
//
//	get_area_details 8f267065-fc1a-59ce-bcf1-6e9335548363
var areaQueries = []map[string]any{
	{"areaId": "8f267065-fc1a-59ce-bcf1-6e9335548363"}, // Stawamus Chief, 32 children
	{"areaId": "fbe1956f-65c2-5515-a26f-127bf15fe598"}, // Grand Wall Boulders, 201 climbs
	{"areaId": "7f74ea62-664e-581e-a929-f01f6bf68f37"}, // Apron Boulders, 55 climbs
	{"areaId": "17a692c8-9e34-5511-90e7-44ef23d10fa1"}, // The Apron, 51 climbs
	{"areaId": "e0d61bef-a560-5b18-88ea-7068dabc2bb2"}, // Olesen Creek Wall, 8 climbs
}

// placeQueries pins the fan-out origins. All five resolve in the compiled-in
// gazetteer, so the lookup itself costs no round trips. They sit on three
// continents so one regional upstream slowdown cannot define the latency
// distribution.
//
// maxDistanceKm is 5 rather than the 20km default: the fan-out is one call per
// crag up to MaxCrags=20, and a tight radius is the only lever that keeps a
// twenty-iteration run inside a few hundred requests.
var placeQueries = []map[string]any{
	{"place": "Squamish", "maxDistanceKm": 5},
	{"place": "Bishop", "maxDistanceKm": 5},
	{"place": "Red River Gorge", "maxDistanceKm": 5},
	{"place": "Fontainebleau", "maxDistanceKm": 5},
	{"place": "Kalymnos", "maxDistanceKm": 5},
}

// climbQueries reuses the same origins. Grade filtering happens after the crags
// are fetched, so it changes the results but not the round-trip count; pinning
// it keeps the samples comparable regardless.
var climbQueries = func() []map[string]any {
	out := make([]map[string]any, 0, len(placeQueries))
	for _, q := range placeQueries {
		c := map[string]any{"minGrade": "5.8", "maxGrade": "5.11a"}
		for k, v := range q {
			c[k] = v
		}
		out = append(out, c)
	}
	return out
}()

// benchGate refuses to run unless the result would be attributable, mirroring
// liveClient in internal/openbeta/live_test.go.
func benchGate(b *testing.B) {
	b.Helper()

	if os.Getenv("OPENBETA_LIVE") == "" {
		b.Skip("set OPENBETA_LIVE=1 to benchmark against the live API")
	}
	if os.Getenv(metricsEnv) == "" {
		b.Fatalf("set %s to an absolute path; the samples are the result, the timings are a summary", metricsEnv)
	}

	b.Logf("run %s", runID())
}

// connectLive wires a client and server over the in-memory transport against
// the real API.
//
// Deliberately not a refactor of connect() in server_test.go: that one stubs
// upstream with httptest and exists to be fast and deterministic, which is the
// opposite of what this needs. Keeping them separate also keeps this file
// behind its build tag.
func connectLive(b *testing.B) *mcp.ClientSession {
	b.Helper()

	server := New(openbeta.New(), "bench")
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		b.Fatalf("connecting server: %v", err)
	}
	b.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "bench-client", Version: "bench"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		b.Fatalf("connecting client: %v", err)
	}
	b.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// samplesSince decodes the records this run appended after offset.
//
// The round-trip counter is installed by the middleware and never visible to
// the client, so the only honest source for roundtrips/op is the dataset
// itself. Reading it back doubles as an assertion that the sink wrote at all.
func samplesSince(b *testing.B, offset int64) []sample {
	b.Helper()

	f, err := os.Open(os.Getenv(metricsEnv))
	if err != nil {
		b.Fatalf("reading back samples: %v", err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, 0); err != nil {
		b.Fatalf("seeking to %d: %v", offset, err)
	}

	var out []sample
	dec := json.NewDecoder(f)
	for dec.More() {
		var s sample
		if err := dec.Decode(&s); err != nil {
			b.Fatalf("decoding sample: %v", err)
		}
		if s.Run == runID() {
			out = append(out, s)
		}
	}
	return out
}

// metricsSize is the current length of the sample file, so a run can find the
// records it added to a file that already holds earlier experiments.
func metricsSize(b *testing.B) int64 {
	b.Helper()

	info, err := os.Stat(os.Getenv(metricsEnv))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		b.Fatalf("sizing sample file: %v", err)
	}
	return info.Size()
}

// runBench drives one tool through its pinned query set.
func runBench(b *testing.B, tool string, queries []map[string]any, maxIters int) {
	b.Helper()
	benchGate(b)

	if b.N > maxIters {
		b.Fatalf("b.N=%d exceeds the %d-call budget for %s; pass -benchtime=%dx", b.N, maxIters, tool, maxIters)
	}

	cs := connectLive(b)
	offset := metricsSize(b)

	b.ResetTimer()
	for i := range b.N {
		// Indexed rather than randomised, so two runs of equal N issue exactly
		// the same queries in the same order.
		args := queries[i%len(queries)]

		ctx, cancel := context.WithTimeout(context.Background(), benchCallTimeout)
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		cancel()
		if err != nil {
			// Protocol failures are a broken harness, not a slow API.
			b.Fatalf("calling %s: %v", tool, err)
		}
	}
	b.StopTimer()

	recorded := samplesSince(b, offset)
	if len(recorded) != b.N {
		b.Fatalf("recorded %d samples for %d calls; the sink missed some", len(recorded), b.N)
	}

	var roundtrips, fails int
	for _, s := range recorded {
		roundtrips += int(s.Roundtrips)
		if s.Err {
			fails++
		}
	}
	b.ReportMetric(float64(roundtrips)/float64(b.N), "roundtrips/op")
	b.ReportMetric(float64(fails)/float64(b.N), "fails/op")
}

func BenchmarkGetAreaDetails(b *testing.B) {
	runBench(b, "get_area_details", areaQueries, maxAreaIters)
}

func BenchmarkCragsNear(b *testing.B) {
	runBench(b, "crags_near", placeQueries, maxCragsIters)
}

func BenchmarkFindClimbs(b *testing.B) {
	runBench(b, "find_climbs", climbQueries, maxClimbsIters)
}
