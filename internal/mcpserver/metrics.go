package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// metricsEnv names the environment variable holding the path to the sample file.
//
// Opt-in rather than on-by-default, because the sink is fed by middleware that
// cannot tell a production tool call from a test one: server_test.go drives real
// MCP calls over an in-memory transport, so an always-on sink wrote fixture
// measurements (stubbed upstream, sub-millisecond) into the same shape as live
// samples. go test never sets this, so the suite records nothing.
const metricsEnv = "OPENBETA_METRICS"

// runEnv optionally pins the run id, so several invocations can be labelled as
// one experiment. Unset means a fresh id per process.
const runEnv = "OPENBETA_RUN"

// sampleVersion stamps every record.
//
// One since the build stamp came out: v0 records carry commit, dirty and go,
// which v1 does not. Provenance is MLflow's job now — it tags every run with the
// commit of the tree the export ran from — so recording it here as well was a
// second source of truth to keep in step.
const sampleVersion = 1

// sample is one measured tool call. Typed rather than a map so the field order
// is declared and a mistyped key fails to compile instead of silently appearing
// in the dataset.
type sample struct {
	V          int             `json:"v"`
	Run        string          `json:"run"`
	TS         time.Time       `json:"ts"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args,omitempty"`
	ArgsSHA    string          `json:"args_sha,omitempty"`
	Roundtrips int32           `json:"roundtrips"`
	MS         float64         `json:"ms"`
	Err        bool            `json:"err"`
}

// metricsMu serialises appends. Encode can issue several writes, so concurrent
// tool calls would otherwise interleave mid-record.
var metricsMu sync.Mutex

// metricsOnce reports the resolved path the first time a sample is written.
// An MCP server's working directory is chosen by whatever launches it, so a
// relative path lands somewhere the operator did not pick — logging the
// absolute path once makes that visible instead of silent.
var metricsOnce sync.Once

// runID groups every sample from one process, so an interrupted run can be told
// apart from a complete one after the fact.
var runID = sync.OnceValue(func() string {
	if id := os.Getenv(runEnv); id != "" {
		return id
	}
	return uuid.NewString()
})

// argsSHA fingerprints a call's arguments so samples can be grouped by query
// without matching on JSON text.
//
// The value is round-tripped through encoding/json first: unmarshalling to a map
// and re-marshalling sorts the keys, so a client that emits its fields in a
// different order still fingerprints the same query. Arguments that do not parse
// are hashed raw — a malformed call is still a sample worth keeping.
func argsSHA(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	canonical := []byte(raw)
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = b
		}
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])[:12]
}

// recordCall appends one measurement, or does nothing if metricsEnv is unset.
//
// Every failure here is logged and swallowed: a metrics sink must never fail a
// tool call or take down the server, which is what the earlier log.Fatal did
// whenever the target directory was missing.
//
// Counting lives in openbeta.CountTransport, which knows about HTTP round
// trips; recording lives here, because what counts as one sample is an MCP tool
// call rather than anything the OpenBeta client knows about.
func recordCall(tool string, args json.RawMessage, start time.Time, elapsed time.Duration, roundtrips int32, failed bool) {
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

	if err := json.NewEncoder(f).Encode(sample{
		V:          sampleVersion,
		Run:        runID(),
		TS:         start.UTC(),
		Tool:       tool,
		Args:       args,
		ArgsSHA:    argsSHA(args),
		Roundtrips: roundtrips,
		// Fractional: locally-validated calls return in well under a
		// millisecond, and integer truncation recorded every one of them as 0.
		MS:  float64(elapsed.Microseconds()) / 1000,
		Err: failed,
	}); err != nil {
		log.Printf("metrics: %v", err)
	}
}
