package openbeta

import (
	"context"
	"net/http"
	"sync/atomic"
)

type ctxKey struct{}

// WithCounter returns a context whose HTTP round trips are counted by n.
func WithCounter(ctx context.Context, n *atomic.Int32) context.Context {
	return context.WithValue(ctx, ctxKey{}, n)
}

type CountTransport struct{ Wrapped http.RoundTripper }

func (t *CountTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if n, ok := req.Context().Value(ctxKey{}).(*atomic.Int32); ok {
		n.Add(1)
	}
	wrapped := t.Wrapped
	if wrapped == nil {
		wrapped = http.DefaultTransport
	}
	return wrapped.RoundTrip(req)
}
