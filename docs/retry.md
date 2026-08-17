# Retry policy for upstream calls

Design note. Not yet implemented.

> **Turn the VPN off before running anything against the live API.**
>
> `api.openbeta.io` sits behind Cloudflare, which treats VPN exit nodes as suspect and challenges
> or drops the request. The symptoms are the same three shapes catalogued below — dropped
> connections, 502s, 503 HTML — so a VPN reads as upstream flakiness rather than as a local
> configuration problem, and no amount of retrying fixes it. Check this first when live tests,
> `scripts/bench.sh` or a manual query start failing in a way that looks transient.

## Why

`api.openbeta.io` sits behind Cloudflare and fails transiently. Three shapes observed in one
session, same bbox that had succeeded minutes earlier:

- `Post "https://api.openbeta.io/graphql": unexpected EOF` — connection dropped mid-response.
- `502` with body `{"data":null,"errors":[{"message":"error code: 502\n"}]}`.
- `503` with an nginx `503 Service Temporarily Unavailable` **HTML page** — including a
  Cloudflare beacon `<script>` — carried inside `errors[].message`.

Each surfaced to the MCP client as a bare tool error. A model consuming that has nothing to act
on, and the user sees a failure for a query that would have worked on the next attempt. The 503
is worse than useless: several hundred characters of markup and a CDN script tag land in the
tool result for the model to read.

The 503 also breaks the neat split assumed below. An infrastructure error arrives wearing GraphQL
clothes — an `errors` array — so the retry decision cannot key on the presence of `errors` alone.
Branch on **HTTP status first**, as `execute` already does, and treat `errors` as authoritative
only on a 2xx. A non-2xx body should never be parsed as a GraphQL envelope.

Whatever surfaces to the caller should be truncated; `truncate(raw, 200)` in
[client.go](../internal/openbeta/client.go) is the right instinct, but the genqlient path does
not currently apply it.

## Where

In an `http.RoundTripper` wrapping the transport, not in `execute` or the tool handlers. The
hand-written client ([client.go](../internal/openbeta/client.go)) and the genqlient client both
take an `*http.Client`, so one transport covers both and survives the migration.

Requests are read-only POSTs (GraphQL queries, no mutations), so replay is safe.

## What to retry

| Condition                               | Retry                                 |
| --------------------------------------- | ------------------------------------- |
| Transport error (EOF, reset, dial)      | yes                                   |
| 502, 503, 504                           | yes                                   |
| 429                                     | yes, honour `Retry-After`             |
| Other 4xx                               | no — our bug, replay repeats it       |
| 2xx with GraphQL `errors`               | no — upstream understood and declined |
| Non-2xx with a GraphQL-shaped body      | yes — status wins; CDN noise, not a verdict |
| `context.Canceled` / `DeadlineExceeded` | no                                    |

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
