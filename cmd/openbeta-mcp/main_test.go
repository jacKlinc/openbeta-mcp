package main

import (
	"bufio"
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

	// A hangup reaches the binary two different ways, and they take different
	// code paths, so both are exercised:
	//
	//   idle       — the server has answered and is waiting. Run returns nil.
	//   mid-message — stdin closes while the server is still working. Run returns
	//                 the SDK's "server is closing" error, which is the only case
	//                 isNormalShutdown's string match ever sees.
	//
	// Only the second can catch an SDK rewording. It was once the whole test and
	// was flaky, because which path you get depends on machine speed — the fix is
	// to accept either outcome as success in both, not to drop one.
	for _, tt := range []struct {
		name             string
		waitForInitReply bool
	}{
		{"hangup while idle", true},
		{"hangup mid-message", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stderr, err := runUntilHangup(t, bin, tt.waitForInitReply)
			if err != nil {
				t.Fatalf("clean client hangup exited non-zero (%v); stderr:\n%s\n"+
					"The SDK likely reworded its shutdown error — update isNormalShutdown to match.",
					err, stderr)
			}
			if !strings.Contains(stderr, "shutting down") {
				t.Errorf("expected a shutdown log line, got:\n%s", stderr)
			}
		})
	}
}

// runUntilHangup starts the binary, sends initialize, closes stdin and reports
// what the process wrote to stderr along with its exit error.
//
// waitForInitReply decides which hangup is simulated: true blocks until the
// initialize response comes back, so stdin closes with the server idle; false
// closes immediately, so it usually closes mid-message.
func runUntilHangup(t *testing.T, bin string, waitForInitReply bool) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Point the binary at an unreachable endpoint so the test never touches the
	// network — initialize does not call upstream.
	cmd := exec.CommandContext(ctx, bin, "-endpoint", "http://127.0.0.1:1/graphql")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting binary: %v", err)
	}

	if _, err := io.WriteString(stdin,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+
			`{"protocolVersion":"2025-06-18","capabilities":{},`+
			`"clientInfo":{"name":"test","version":"1"}}}`+"\n"); err != nil {
		t.Fatalf("writing initialize: %v", err)
	}

	// Handing the message over is not the same as the server having processed it,
	// so reading the reply is what makes "idle" mean idle.
	if waitForInitReply && !bufio.NewScanner(stdout).Scan() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("no initialize response before hangup; stderr:\n%s", stderr.String())
	}

	// Closing stdin is exactly what a client hanging up looks like.
	if err := stdin.Close(); err != nil {
		t.Fatalf("closing stdin: %v", err)
	}

	// Wait first: it blocks until the goroutine copying the child's stderr into
	// the builder has finished, so reading the builder before it is both a data
	// race and liable to miss the last line.
	waitErr := cmd.Wait()
	return stderr.String(), waitErr
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
