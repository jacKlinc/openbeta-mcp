package openbeta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is the public OpenBeta GraphQL API. Read-only, unauthenticated.
const DefaultEndpoint = "https://api.openbeta.io/graphql"

// defaultTimeout bounds every upstream call so a hung API cannot wedge the
// server (NFR-4). Sized against observed upstream latency: simple queries return
// in ~150ms, cragsWithin with climb counts in ~1.2s.
const defaultTimeout = 30 * time.Second

// Client queries the OpenBeta GraphQL API over net/http. No codegen, no GraphQL
// library — two hand-written queries do not justify the build step (NFR-7).
type Client struct {
	endpoint string
	http     *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithEndpoint overrides the GraphQL endpoint, mainly for tests.
func WithEndpoint(url string) Option {
	return func(c *Client) { c.endpoint = url }
}

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New returns a Client pointed at the public API unless overridden.
func New(opts ...Option) *Client {
	c := &Client{
		endpoint: DefaultEndpoint,
		http:     &http.Client{Timeout: defaultTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// graphQLError is one entry in a GraphQL response's errors array.
type graphQLError struct {
	Message string `json:"message"`
}

// APIError reports errors returned by the GraphQL layer itself — the request
// reached the API and it declined. Distinct from transport failures so callers
// can tell "upstream said no" from "could not reach upstream" (FR-16, FR-17).
type APIError struct {
	Messages []string
}

func (e *APIError) Error() string {
	return "openbeta graphql error: " + strings.Join(e.Messages, "; ")
}

// execute runs one GraphQL query and decodes data into out.
func (c *Client) execute(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": vars,
	})
	if err != nil {
		return fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("calling openbeta api: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	// A GraphQL error is reported in the body with a 200, but the API also
	// returns bare non-2xx pages (a 502 on malformed queries), which will not
	// parse as JSON. Check status first so the error names the real cause.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("openbeta api returned %s: %s", resp.Status, truncate(string(raw), 200))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decoding response: %w (body: %s)", err, truncate(string(raw), 200))
	}

	if len(envelope.Errors) > 0 {
		msgs := make([]string, len(envelope.Errors))
		for i, e := range envelope.Errors {
			msgs[i] = e.Message
		}
		return &APIError{Messages: msgs}
	}

	if len(envelope.Data) == 0 {
		return fmt.Errorf("openbeta api returned no data and no errors")
	}

	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decoding data: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
