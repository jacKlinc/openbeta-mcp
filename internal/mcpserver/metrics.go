package mcpserver

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// metricsEnv names the environment variable holding the path to the sample file.
//
// Opt-in rather than on-by-default, because the sink is fed by middleware that
// cannot tell a production tool call from a test one: server_test.go drives real
// MCP calls over an in-memory transport, so an always-on sink wrote fixture
// measurements (stubbed upstream, sub-millisecond) into the same shape as live
// samples. go test never sets this, so the suite now records nothing.
const metricsEnv = "OPENBETA_METRICS"

// metricsMu serialises appends. Encode can issue several writes, so concurrent
// tool calls would otherwise interleave mid-record.
var metricsMu sync.Mutex

// metricsOnce reports the resolved path the first time a sample is written.
// An MCP server's working directory is chosen by whatever launches it, so a
// relative path lands somewhere the operator did not pick — logging the
// absolute path once makes that visible instead of silent.
var metricsOnce sync.Once

// recordCall appends one measurement, or does nothing if metricsEnv is unset.
//
// Every failure here is logged and swallowed: a metrics sink must never fail a
// tool call or take down the server, which is what the earlier log.Fatal did
// whenever the target directory was missing.
//
// Counting lives in openbeta.CountTransport, which knows about HTTP round
// trips; recording lives here, because what counts as one sample is an MCP tool
// call rather than anything the OpenBeta client knows about.
func recordCall(tool string, start time.Time, elapsed time.Duration, roundtrips int32, failed bool) {
	path := os.Getenv(metricsEnv)
	if path == "" {
		return
	}

	metricsMu.Lock()
	defer metricsMu.Unlock()

	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	metricsOnce.Do(func() { log.Printf("metrics: recording tool calls to %s", path) })

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("metrics: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("metrics: %v", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(map[string]any{
		"ts":         start.UTC(),
		"tool":       tool,
		"roundtrips": roundtrips,
		// Fractional: locally-validated calls return in well under a
		// millisecond, and integer truncation recorded every one of them as 0.
		"ms":  float64(elapsed.Microseconds()) / 1000,
		"err": failed,
	}); err != nil {
		log.Printf("metrics: %v", err)
	}
}
