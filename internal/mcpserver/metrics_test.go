package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decodeSamples reads every record written to path.
func decodeSamples(t *testing.T, path string) []sample {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening samples: %v", err)
	}
	defer f.Close()

	var out []sample
	dec := json.NewDecoder(f)
	for dec.More() {
		var s sample
		if err := dec.Decode(&s); err != nil {
			t.Fatalf("decoding sample %d: %v", len(out)+1, err)
		}
		out = append(out, s)
	}
	return out
}

func TestRecordCallWritesSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.jsonl")
	t.Setenv(metricsEnv, path)

	args := json.RawMessage(`{"areaId":"8f267065-fc1a-59ce-bcf1-6e9335548363"}`)
	recordCall("get_area_details", args, time.Now(), 150*time.Millisecond, 1, false)

	samples := decodeSamples(t, path)
	if len(samples) != 1 {
		t.Fatalf("wrote %d samples, want 1", len(samples))
	}
	got := samples[0]

	if got.V != sampleVersion {
		t.Errorf("v = %d, want %d", got.V, sampleVersion)
	}
	if got.Tool != "get_area_details" {
		t.Errorf("tool = %q", got.Tool)
	}
	if got.Roundtrips != 1 {
		t.Errorf("roundtrips = %d, want 1", got.Roundtrips)
	}
	if got.MS != 150 {
		t.Errorf("ms = %v, want 150", got.MS)
	}
	if got.Err {
		t.Error("err = true, want false")
	}
	if got.Run == "" {
		t.Error("run is empty; samples from one process cannot be grouped")
	}
	// Verbatim, so a later reader can re-run the exact query.
	if string(got.Args) != string(args) {
		t.Errorf("args = %s, want %s", got.Args, args)
	}
	if len(got.ArgsSHA) != 12 {
		t.Errorf("args_sha = %q, want 12 hex chars", got.ArgsSHA)
	}
	if strings.TrimLeft(got.ArgsSHA, "0123456789abcdef") != "" {
		t.Errorf("args_sha = %q, want hex", got.ArgsSHA)
	}
}

// The sink stays silent unless asked, which is what keeps server_test.go's tool
// calls out of the dataset.
func TestRecordCallDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(metricsEnv, "")

	recordCall("crags_near", json.RawMessage(`{"place":"Squamish"}`), time.Now(), time.Second, 21, false)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("wrote %d files with the sink disabled, want 0", len(entries))
	}
}

func TestRecordCallCapturesFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.jsonl")
	t.Setenv(metricsEnv, path)

	recordCall("find_climbs", json.RawMessage(`{"minGrade":"V4"}`), time.Now(), 400*time.Microsecond, 0, true)

	samples := decodeSamples(t, path)
	if len(samples) != 1 {
		t.Fatalf("wrote %d samples, want 1", len(samples))
	}
	if !samples[0].Err {
		t.Error("err = false, want true for a rejected call")
	}
	// Sub-millisecond calls are exactly what integer truncation erased, and a
	// validation rejection is the common case.
	if samples[0].MS != 0.4 {
		t.Errorf("ms = %v, want 0.4", samples[0].MS)
	}
}

// A client is free to emit object keys in any order, so the fingerprint has to
// describe the query rather than its serialisation.
func TestArgsSHAIsOrderIndependent(t *testing.T) {
	a := argsSHA(json.RawMessage(`{"a":1,"b":2}`))
	b := argsSHA(json.RawMessage(`{"b":2,"a":1}`))
	if a != b {
		t.Errorf("key order changed the fingerprint: %q vs %q", a, b)
	}

	if c := argsSHA(json.RawMessage(`{"a":1,"b":3}`)); c == a {
		t.Error("different values share a fingerprint")
	}
	if got := argsSHA(nil); got != "" {
		t.Errorf("argsSHA(nil) = %q, want empty", got)
	}
	// Unparseable arguments still belong in the dataset.
	if got := argsSHA(json.RawMessage(`{not json`)); len(got) != 12 {
		t.Errorf("argsSHA(malformed) = %q, want 12 hex chars", got)
	}
}
