package openbeta

import (
	"net/http"
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

// Endpoint reports the GraphQL endpoint this Client targets.
//
// Exposed so callers building a genqlient client can inherit the configured
// endpoint rather than reaching for DefaultEndpoint — that is what made
// WithEndpoint silently ineffective, and tests hit the live API.
func (c *Client) Endpoint() string { return c.endpoint }

// HTTPClient reports the underlying HTTP client, carrying the configured
// timeout (and, once added, the retrying transport — see docs/retry.md).
func (c *Client) HTTPClient() *http.Client { return c.http }

// New returns a Client pointed at the public API unless overridden.
func New(opts ...Option) *Client {
	// TODO: add graphql here?
	c := &Client{
		endpoint: DefaultEndpoint,
		http:     &http.Client{Timeout: defaultTimeout},
	}
	for _, o := range opts {
		o(c)
	}
	// Wrapped after the options run, not before: WithHTTPClient replaces the whole
	// *http.Client, so installing the counter in the literal above lets that option
	// silently drop it. Note this mutates a caller-supplied client.
	c.http.Transport = &CountTransport{Wrapped: c.http.Transport}
	return c
}
