# OpenBeta MCP Server — POC Design Plan

Requirements extracted from this document live in
[functional-requirements.md](functional-requirements.md) and
[non-functional-requirements.md](non-functional-requirements.md). This file remains the
design-rationale record — the "why" behind those requirements.

Several of its findings were later corrected by introspection against the live API — the
`totalClimbs` filter, the zoom behaviour, and the claim that no point/radius resolver exists. See
[graphql-findings.md](graphql-findings.md) for the evidence.

Context: [RFC #487](https://github.com/OpenBeta/openbeta-graphql/issues/487) proposes an official
TypeScript/stdio MCP server for OpenBeta. This is a third-party Go POC, not an attempt to preempt
that RFC. Built on the public GraphQL API at `https://api.openbeta.io`.

## Scope

Two read-only tools, one Go MCP server, stdio transport:

- `get_area_details(areaId)` — detail for a single area/crag by UUID
- `crags_within(bbox, zoom)` — crags inside a bounding box

`search_climbs` from the original RFC is dropped — decided it wasn't a good tool shape.

No caching layer for this pass. Decided against DynamoDB-backed caching until the upstream cost of
`cragsWithin` is actually measured — designing a cache key/TTL strategy around an unmeasured query
was premature. Revisit once real latency/frequency data exists.

**Success signal:** correctness — right data back for real queries. Not perf, not cache hit rate.

## Why Go, not TypeScript

Not proposed as a replacement for an official TS package — RFC #487 is TS/AGPL and that's a
reasonable fit for an `npx @openbeta/mcp`-style official release. This is a parallel third-party
build. Rationale documented in conversation:

- Performance/cold-start argument is weak for this workload — proxy calls are I/O-bound against
  upstream GraphQL (150–600ms), language overhead is noise by comparison. Confirmed by Jack's own
  Lambda Bench project (Go cold starts ~3x faster, warm-start parity at scale).
- Real reasons: single static binary, no Node runtime dependency, and it's a relevant Go portfolio
  piece for current job search (Elastic Path Cloud Eng role uses Go/AWS/IaC).

## Hosting question (deferred)

Raised in the GitHub comment as an open question rather than a decision: stdio is MCP convention
and requires no infra, but a hosted server with a shared cache could in principle serve many users
from one cache instead of N uncached local processes. Not pursued for this POC — no cache layer
yet, see above. Revisit if/when caching is added.

If revisited: same tool/handler logic works over both `mcp.StdioTransport` and a streamable-HTTP
transport in the Go SDK — transport is a thin outer layer, not a fork of the logic.

## GraphQL findings (verified against live API)

### `cragsWithin`

Input type: `SearchWithinFilter`

```graphql
input SearchWithinFilter {
  bbox: [Float]   # [minLng, minLat, maxLng, maxLat]
  zoom: Float
}
```

Confirmed via introspection and a live query against a Squamish-area bbox
(`[-123.2, 49.6, -122.9, 49.8]`). Ordering confirmed empirically: `minLng, minLat, maxLng, maxLat`.

Returns area-hierarchy nodes inside the bbox at any level (crag, boulder field, sector, parent
region) — not just leaf crags. In the Squamish test, 9 of 21 results had `totalClimbs: 0`
(organizational parents like "Squamish", "Falls Area", "Squamish Ice & Mixed").

**Decision:** filter `totalClimbs == 0` out in the Go handler before returning to the LLM. The
schema doesn't support filtering server-side, so this happens client-side after the fetch.

Fields returned (from live query): `areaName`, `totalClimbs`, `uuid`, `metadata { lat lng }`.

```go
type CragsWithinResponse struct {
    Data struct {
        CragsWithin []Crag `json:"cragsWithin"`
    } `json:"data"`
}

type Crag struct {
    UUID        string   `json:"uuid"`
    AreaName    string   `json:"areaName"`
    TotalClimbs int      `json:"totalClimbs"`
    Metadata    Metadata `json:"metadata"`
}

type Metadata struct {
    Lat float64 `json:"lat"`
    Lng float64 `json:"lng"`
}
```

Trimmed tool output type (separate from the wire struct):

```go
type CragSummary struct {
    UUID        string  `json:"uuid"`
    Name        string  `json:"name"`
    Lat         float64 `json:"lat"`
    Lng         float64 `json:"lng"`
    TotalClimbs int     `json:"totalClimbs"`
}
```

Handler sketch:

```go
func handleCragsWithin(ctx context.Context, args CragsWithinArgs) ([]CragSummary, error) {
    resp, err := queryCragsWithin(ctx, args.Bbox, args.Zoom)
    if err != nil {
        return nil, err
    }

    var out []CragSummary
    for _, c := range resp.Data.CragsWithin {
        if c.TotalClimbs == 0 {
            continue
        }
        out = append(out, CragSummary{
            UUID:        c.UUID,
            Name:        c.AreaName,
            Lat:         c.Metadata.Lat,
            Lng:         c.Metadata.Lng,
            TotalClimbs: c.TotalClimbs,
        })
    }
    return out, nil
}
```

Note: `cragsWithin` is bbox+zoom shaped (map-viewport pattern), not point+radius. A future
proximity-search tool would need to synthesize a bbox from a center point + radius client-side —
there is no dedicated point/radius resolver on the schema as far as confirmed so far.

### `get_area_details`

**Not yet finalized.** Next step: introspect `AreaType` fields and/or run a live query against a
known UUID (e.g. Stawamus Chief: `8f267065-fc1a-59ce-bcf1-6e9335548363`, from the `cragsWithin`
result above) to confirm the shape, minimum needed: area name, coordinates (likely under the same
`metadata { lat lng }` pattern as `cragsWithin`), and child routes/climbs.

## GraphQL client approach

`net/http` + manual request/response structs per query. Decided against a codegen client
(genqlient) — the setup cost (schema file + build step) isn't worth it for two hand-written
queries at this stage. Revisit if the query surface grows.

## Comment posted to RFC #487

Posted as a new-to-the-repo, non-prescriptive comment:

- Disclosed finding the repo/issue same-day
- Praised the beta-disclaimer requirement in the RFC
- Raised hosted+cache as a possible answer to the RFC's own rate-limiting concern, framed as a
  question/option rather than a pushback on stdio
- Noted the free-tier Lambda + Function URL + DynamoDB path, and that per-client throttling would
  need WAF rate-based rules (API Gateway usage plans require API keys, which cuts against a
  frictionless local install)
- Noted transport and tool logic are separable, so stdio-vs-hosted doesn't have to be an either/or
- Asked about `cragsWithin` (confirmed to exist, see above) and whether a hosted option had any
  appetite from maintainers
- Repo context at time of writing: RFC opened by a non-maintainer (`author_association: NONE`),
  zero comments, no activity since filing; last commit to default branch ~7 months prior, by the
  primary active maintainer (clintonlunn)

## Open items / next steps

1. Confirm `get_area_details` / `AreaType` field shape (introspection or live query)
2. Write the Go MCP server: register both tools on one `mcp.Server`, run via `mcp.StdioTransport`
3. Manual GraphQL structs + `net/http` client for both queries
4. Decide later, once measured: is `cragsWithin` cheap enough upstream that caching isn't worth the
   complexity, or does it justify revisiting the DynamoDB approach