package mcpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
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
// Zero because the schema is unreleased: nothing that writes it has been merged,
// so there is no compatibility to keep and no earlier shape to migrate from. The
// field exists to give the first breaking change after it ships something to key
// on.
const sampleVersion = 0

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
	Commit     string          `json:"commit"`
	Dirty      bool            `json:"dirty"`
	Go         string          `json:"go"`
}

// metricsMu serialises appends. Encode can issue several writes, so concurrent
// tool calls would otherwise interleave mid-record.
var metricsMu sync.Mutex

// metricsOnce reports the resolved path the first time a sample is written.
// An MCP server's working directory is chosen by whatever launches it, so a
// relative path lands somewhere the operator did not pick — logging the
// absolute path once makes that visible instead of silent.
var metricsOnce sync.Once

// stamp identifies the build that served a call.
type stamp struct {
	Commit string
	Dirty  bool
	Go     string
}

// buildStamp reads the VCS information the toolchain embeds at link time.
//
// The commit has to come from the binary rather than from `git rev-parse` at
// analysis time: a running server can be built from a commit the working tree
// has long since moved past, which is exactly what the pilot samples turned out
// to be. Dirty is recorded rather than hidden — a hash from a modified tree does
// not identify the code that ran.
//
// Commit is left empty when the binary carries no stamp. `go test` binaries are
// test-only packages, which the default -buildvcs=auto excludes, so benchmarks
// must pass -buildvcs=true; the bench gate refuses to run without it rather than
// letting this silently record nothing.
var buildStamp = sync.OnceValue(func() stamp {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return stamp{}
	}
	s := stamp{Go: info.GoVersion}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			s.Commit = setting.Value
		case "vcs.modified":
			s.Dirty = setting.Value == "true"
		}
	}
	return s
})

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

	build := buildStamp()
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
		MS:     float64(elapsed.Microseconds()) / 1000,
		Err:    failed,
		Commit: build.Commit,
		Dirty:  build.Dirty,
		Go:     build.Go,
	}); err != nil {
		log.Printf("metrics: %v", err)
	}
}
