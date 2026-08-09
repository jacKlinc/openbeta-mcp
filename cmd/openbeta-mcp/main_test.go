package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This is the regression guard for isNormalShutdown's message matching.
//
// It builds and runs the actual binary, because that is the only place the
// shutdown error appears: an in-process IOTransport over io.Pipe returns nil on
// hangup, so it would pass without exercising anything. A client establishes a
// session and hangs up; the process must exit 0.
//
// If an SDK upgrade rewords the error, this fails loudly instead of the binary
// quietly going back to exiting 1 on every clean disconnect.
func TestNormalShutdownExitsZero(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run test in short mode")
	}

	bin := filepath.Join(t.TempDir(), "openbeta-mcp")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building binary: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A session must exist before the SDK reports hangup as an error; an
	// immediate EOF returns nil and would not exercise the check. Point the
	// binary at an unreachable endpoint so the test never touches the network —
	// initialize does not call upstream.
	cmd := exec.CommandContext(ctx, bin, "-endpoint", "http://127.0.0.1:1/graphql")
	cmd.Stdin = strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"protocolVersion":"2025-06-18","capabilities":{},` +
			`"clientInfo":{"name":"test","version":"1"}}}` + "\n")

	var stderr strings.Builder
	cmd.Stderr = &stderr

	// Stdin hits EOF as soon as that one message is read — exactly what a client
	// hanging up looks like.
	err := cmd.Run()
	if err != nil {
		t.Fatalf("clean client hangup exited non-zero (%v); stderr:\n%s\n"+
			"The SDK likely reworded its shutdown error — update isNormalShutdown to match.",
			err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "shutting down") {
		t.Errorf("expected a shutdown log line, got:\n%s", stderr.String())
	}
}

func TestIsNormalShutdown(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"sdk server closing", errors.New("server is closing: EOF"), true},
		{"sdk client closing", errors.New("client is closing: EOF"), true},
		{"bare EOF", io.EOF, true},
		{"wrapped EOF", errors.Join(errors.New("read failed"), io.EOF), true},
		{"sdk connection closed", mcp.ErrConnectionClosed, true},
		{"genuine failure", errors.New("address already in use"), false},
		{"malformed message", errors.New("invalid JSON in request"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNormalShutdown(tt.err); got != tt.want {
				t.Errorf("isNormalShutdown(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// Diagnostics must never reach stdout — it carries the protocol stream, and a
// stray write corrupts the session (FR-2).
func TestDiagnosticsGoToStderr(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	for _, bad := range []string{"fmt.Print(", "fmt.Printf(", "os.Stdout"} {
		// The -version flag legitimately prints to stdout before any session
		// exists, so fmt.Println is expected; the rest are not.
		if strings.Contains(string(src), bad) {
			t.Errorf("main.go writes to stdout via %s; diagnostics belong on stderr", bad)
		}
	}
	if !strings.Contains(string(src), "log.SetOutput(os.Stderr)") {
		t.Error("main.go does not pin logging to stderr")
	}
}
