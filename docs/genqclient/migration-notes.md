# genqlient migration: open items

Notes recorded while switching `get_area_details` from the hand-written client to
the generated one. The trade is deliberate: the generated path deleted ~122 lines
from `area.go` and puts another ~80 in reach once `Client.execute` goes. Nothing
below argues against the migration — these are the things that migration moved,
broke, or deferred, so they don't get lost.

Status: `get_area_details` is on the generated client. `crags_within` is not.

## Fix before wider use

Both are leftovers from the scratch runner the handler grew out of, in
`handleGetAreaDetails` in `internal/mcpserver/server.go`.

**`log.Fatal(err)` on the upstream error.** This calls `os.Exit(1)` — the whole
MCP server process dies on any failed lookup, taking the session's stdio
connection with it. A single bad UUID is enough. The `if err != nil` block below
it is unreachable while this stands. It should return the error so the SDK packs
it as a tool error (FR-16, FR-17, and the tool-error contract asserted in
`TestUpstreamErrorIsAToolError`).

**`context.Background()` passed to `generated.GetAreaDetails`.** The handler
receives a `ctx` and drops it, so client cancellation and deadlines don't reach
the request. Pass the handler's `ctx`.

## What `Client.execute` is carrying

Deleting it is right — genqlient's `graphql.Client` does the same POST. But
`execute` accumulated behaviour that isn't in the generic path, and each piece
needs a home before it goes.

**Timeout.** `defaultTimeout` (30s, NFR-4) lives on the `*http.Client` inside
`Client`. `graphql.NewClient` takes an `*http.Client`, so this survives — but
only if the caller passes a configured one. The current handler passes
`http.DefaultClient`, which has **no timeout at all**. A hung API now wedges the
handler indefinitely. This is a live regression, not a hypothetical.

**Endpoint override.** `WithEndpoint` is what lets `server_test.go` point the
whole server at an `httptest` stub. The handler constructs its own client against
`openbeta.DefaultEndpoint` and ignores the injected `*openbeta.Client` entirely,
so that seam is currently bypassed for `get_area_details` — the test stub no
longer intercepts it.

**Error classification.** `APIError` distinguishes "upstream said no" from "could
not reach upstream", and the non-2xx check exists because the API returns bare
502 pages that don't parse as JSON. genqlient returns `gqlerror.List` for GraphQL
errors and a wrapped transport error otherwise; the distinction is recoverable
but the mapping has to be written.

**`ErrAreaNotFound`.** OpenBeta returns a GraphQL error, not a null, for an
unknown UUID (`area Area <uuid> not found`). The typed error currently comes from
inspecting the decoded response; on the generated path it has to come from
matching the error message.

Suggested shape: keep `Client` as the seam, give it a `gql graphql.Client` field
built from the same endpoint and `*http.Client`, and keep the error mapping in
one place. That preserves `WithEndpoint`/`WithHTTPClient` and the tests, and
still deletes `execute`.

## Accepted trade-offs

Recorded as decisions, not bugs.

**Payload size.** The generated response is roughly 40% larger for the same
information: `metadata{lat,lng,leaf}` per child instead of flat coordinates, no
`omitempty`, all seven grade systems per climb, ten discipline booleans. Judged
an acceptable cost against the deleted mapping layer. Note it grows with climb
count — an area with 100 routes is where it would bite, and Stawamus Chief
returns no climbs at all.

**`totalClimbs` is now exposed raw.** `docs/graphql-findings.md` records that it
is unreliable: 143 of 176 crags in the Squamish bbox report 0 while holding
climbs. In the live `get_area_details` response Tantalus Wall reports 0 against
8 actual climbs. `crags_within` still computes counts from the climbs array
(`crags_within.go`), so the two tools now disagree about the same number. If
`crags_within` migrates too, that logic must survive — the raw field cannot
replace it.

**Field naming.** genqlient title-cases GraphQL fields mechanically: `Uuid`,
`Fa`, `Yds`, `Vscale`, `Tr`. It has no initialism list and can't know `tr` means
top rope. Fixable per-field with GraphQL aliases in the query (`topRope: tr`),
not with Go-side renames — those get overwritten on regeneration.

**Nullable fields are non-pointer.** The schema types `area(uuid: ID): Area` as
nullable; the generated `Area` is a value, so a null would decode as a zero-value
struct rather than nil. Set `optional: pointer` in `genqlient.yaml` if that
distinction is ever needed. Currently masked by the API erroring instead of
returning null.

## Output types are not affected

`CragSummary`, `AreaDetail`, `ClimbSummary` and `ChildArea` in `types.go` are the
MCP-facing schema, deliberately separate from wire types (NFR-10, NFR-11).
genqlient replaces wire types only. Returning generated types directly from a
handler — which is what `get_area_details` now does — couples the tool's public
output schema to the upstream schema, so a schema refresh can reshape what
clients see. Acceptable for a POC; worth reversing before anything depends on
the output shape.
