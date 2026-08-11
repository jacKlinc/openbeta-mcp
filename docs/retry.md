# Retry policy for upstream calls

Design note. Not yet implemented.

## Why

`api.openbeta.io` sits behind Cloudflare and fails transiently. Both shapes observed in one
session, same bbox that had succeeded minutes earlier:

- `Post "https://api.openbeta.io/graphql": unexpected EOF` — connection dropped mid-response.
- `502` with body `{"data":null,"errors":[{"message":"error code: 502\n"}]}`.

Each surfaced to the MCP client as a bare tool error. A model consuming that has nothing to act
on, and the user sees a failure for a query that would have worked on the next attempt.

## Where

In an `http.RoundTripper` wrapping the transport, not in `execute` or the tool handlers. The
hand-written client ([client.go](../internal/openbeta/client.go)) and the genqlient client both
take an `*http.Client`, so one transport covers both and survives the migration.

Requests are read-only POSTs (GraphQL queries, no mutations), so replay is safe.

## What to retry

| Condition                            | Retry |
| ------------------------------------ | ----- |
| Transport error (EOF, reset, dial)   | yes   |
| 502, 503, 504                        | yes   |
| 429                                  | yes, honour `Retry-After` |
| Other 4xx                            | no — our bug, replay repeats it |
| 200 with GraphQL `errors`            | no — upstream understood and declined |
| `context.Canceled` / `DeadlineExceeded` | no |

## Budget

- 2 retries, 3 attempts total.
- Backoff 250ms, 1s, full jitter.
- Bounded by the caller's context; the existing 30s client timeout is the ceiling for all
  attempts combined, so a slow first attempt leaves no budget and that is correct.

Sized so the worst case stays inside an interactive tool call. Deeper retries belong to the MCP
client, which can tell the user something is wrong.

## Body handling

`execute` already reads the body before branching on status. A retrying transport must drain and
close the discarded response, or connections leak. Request bodies need `GetBody` set for replay —
`http.NewRequestWithContext` with a `*bytes.Reader` sets it automatically.

## Tests

`httptest` server failing N times then succeeding, per row of the table above. Assert attempt
counts, not sleeps — inject the clock or keep backoff at zero under test.
