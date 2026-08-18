// Command openbeta-mcp serves the OpenBeta climbing database over MCP on stdio.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/mcpserver"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
)

// version is overridable at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags)" ./cmd/openbeta-mcp
var version = "dev"

// defaultEndpoint resolves the endpoint the -endpoint flag falls back to, so
// precedence reads flag > OPENBETA_ENDPOINT > DefaultEndpoint.
//
// An env var rather than a dotenv file because that is how a stdio MCP server is
// configured in the first place: the client config block passes `env` to the
// subprocess, so this is the native mechanism and a .env would be a second,
// redundant one. Resolution happens here at the composition root rather than in
// internal/ — the client stays pure config and is handed a URL.
//
// Unset is always valid. `go install` then run with nothing set must keep
// talking to production; that is the property worth protecting.
func defaultEndpoint() string {
	if v := os.Getenv("OPENBETA_ENDPOINT"); v != "" {
		return v
	}
	return openbeta.DefaultEndpoint
}

func main() {
	endpoint := flag.String("endpoint", defaultEndpoint(), "OpenBeta GraphQL endpoint (or set OPENBETA_ENDPOINT)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	// stdout carries the MCP protocol stream, so every diagnostic goes to stderr.
	// A stray Println here would corrupt the session (FR-2).
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("openbeta-mcp: ")

	// Ctrl-C and SIGTERM cancel the context, which unblocks Run and lets the
	// session close cleanly rather than dying mid-message.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := openbeta.New(openbeta.WithEndpoint(*endpoint))
	server := mcpserver.New(client, version)

	log.Printf("serving %s on stdio (version %s)", *endpoint, version)
	err := server.Run(ctx, &mcp.StdioTransport{})
	// A hangup surfaces two different ways depending on what the server was doing
	// when stdin closed: idle, Run returns nil; mid-message, it returns the SDK's
	// "server is closing" error. A cancelled context is ordinary shutdown too.
	// All three are the same event and get the same log line — the nil case is
	// listed first so isNormalShutdown is never handed a nil error.
	switch {
	case err == nil, ctx.Err() != nil, isNormalShutdown(err):
		log.Print("shutting down")
	default:
		log.Fatalf("server failed: %v", err)
	}
}

// isNormalShutdown reports whether err is the SDK's way of saying the client
// closed the connection.
//
// An MCP client shuts a stdio server down by closing its stdin, and once a
// session is live the SDK surfaces that as an error rather than a nil return —
// so without this check, every clean exit reports failure and exits 1, which
// supervisors and client logs read as a crash.
//
// Matching on the message is unfortunate but forced: the SDK's sentinel is
// jsonrpc2.ErrServerClosing in its internal/ tree, so it can be neither
// imported nor reconstructed for errors.Is, and the *jsonrpc2.WireError it
// unwraps to is equally unexported. TestNormalShutdownIsNotAnError pins this
// against SDK upgrades — if the wording changes, that test fails rather than
// this silently regressing to exit 1.
func isNormalShutdown(err error) bool {
	return strings.Contains(err.Error(), "server is closing") ||
		strings.Contains(err.Error(), "client is closing") ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, mcp.ErrConnectionClosed)
}
