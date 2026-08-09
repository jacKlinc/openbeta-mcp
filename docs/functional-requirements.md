# Functional Requirements — OpenBeta MCP Server (POC)

What the server must do. Design rationale for these decisions lives in [plan.md](plan.md); this
document carries only the "what", stated so each item can be verified.

Requirement IDs are stable once assigned — if a requirement is dropped, mark it withdrawn rather
than renumbering the rest.

Scope: a third-party Go MCP server over the public OpenBeta GraphQL API at `https://api.openbeta.io`.

---

## 1. Server and protocol

**FR-1** — The server registers exactly two tools on a single `mcp.Server` instance:
`crags_within` and `get_area_details`.

**FR-2** — The server communicates over stdio transport (`mcp.StdioTransport`), speaking MCP on
stdin/stdout. Diagnostic output must go to stderr, never stdout, so it cannot corrupt the protocol
stream.

**FR-3** — Both tools are read-only. No tool issues a GraphQL mutation or otherwise modifies
upstream state.

**FR-4** — Each tool declares a name, description, and input schema sufficient for an LLM client to
call it without out-of-band documentation.

---

## 2. `crags_within(bbox, zoom)`

Returns climbing areas inside a geographic bounding box.

**FR-5** — The tool accepts two arguments:

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `bbox` | array of 4 floats | yes | `[minLng, minLat, maxLng, maxLat]` |
| `zoom` | float | no — defaults to 13 | map zoom level, passed through to upstream |

`zoom` defaults to 13 rather than being required. Leaving it out must not silently return parent
regions: the default has to sit at or above the leaf threshold in FR-9a, and 13 clears it. A caller
asking "what can I climb here" wants crags.

**FR-6** — The `bbox` element order is `minLng, minLat, maxLng, maxLat` — longitude first. This
ordering was confirmed empirically against the live API, not read off the schema (the schema types
it only as `[Float]`). The tool description exposed to the LLM must state the ordering explicitly.

**FR-7** — The tool maps to the upstream `cragsWithin` query, passing `bbox` and `zoom` as a
`SearchWithinFilter` input:

```graphql
input SearchWithinFilter {
  bbox: [Float]
  zoom: Float
}
```

**FR-8** — The tool requests `uuid`, `areaName`, `totalClimbs`, `pathTokens`,
`metadata { lat lng leaf isBoulder }`, and `climbs { uuid }`.

`climbs { uuid }` is requested solely to count climbs — see FR-9. It is the reason this query costs
~1.2s instead of ~0.13s. That cost buys correctness, which is the stated success criterion (NFR-1).

**FR-9** — Results holding no climbs are filtered out in the Go handler before returning. An area
holds climbs if `len(climbs) > 0`, or failing that if `totalClimbs > 0`.

**`totalClimbs` alone must not be used for this test.** It reads `0` on the large majority of leaf
crags that demonstrably have climbs. Measured against the Squamish bbox
(`[-123.2, 49.6, -122.9, 49.8]`) at zoom 13: of 180 results, 176 hold at least one climb, but only
33 report `totalClimbs > 0`. A `totalClimbs == 0` filter would discard **143 real crags**, including
Neat and Cool (39 climbs), Fern Hill (31), Slhanay (29) and Tantalus Wall (8).

The two-step test is needed because each field is reliable in the case the other is not:

| Area kind | `climbs` | `totalClimbs` | Count from |
| --- | --- | --- | --- |
| Leaf crag | populated | usually `0` | `len(climbs)` |
| Parent area | always `[]` | aggregates descendants | `totalClimbs` |

The filter is client-side because the schema has no server-side equivalent — `SearchWithinFilter`
exposes only `bbox` and `zoom`. This is a constraint, not an oversight; do not replace it with a
query parameter without first confirming the schema has gained one.

**FR-9a** — `zoom` selects which level of the hierarchy the API returns, and the tool must document
this rather than treat zoom as a free parameter:

- **zoom ≤ 10** — organizational parent areas (`metadata.leaf: false`). 22 results for the Squamish
  bbox, identical at zoom 6, 8, 9 and 10.
- **zoom ≥ 11** — individual crags (`metadata.leaf: true`). 180 results, identical at zoom 11 and 13.

The cutover is a property of the upstream resolver, not of the bbox. Zoom 11+ is the right default
for a "what can I climb here" question.

This also corrects the original observation in [plan.md](plan.md), which was taken at low zoom:
the parent areas seen there are not mixed into leaf results, they are what low zoom returns
*instead* of leaf results.

**FR-10** — The tool returns a trimmed summary type, distinct from the GraphQL wire struct:

```go
type CragSummary struct {
    UUID       string   `json:"uuid"`
    Name       string   `json:"name"`
    Lat        float64  `json:"lat"`
    Lng        float64  `json:"lng"`
    ClimbCount int      `json:"climbCount"`
    IsBoulder  bool     `json:"isBoulder,omitempty"`
    Path       []string `json:"path,omitempty"`
}
```

Field mapping from the wire type: `uuid` → `UUID`, `areaName` → `Name`, `metadata.lat` → `Lat`,
`metadata.lng` → `Lng`, `metadata.isBoulder` → `IsBoulder`, `pathTokens` → `Path`. `ClimbCount` is
derived per FR-9 — it is deliberately *not* named `totalClimbs`, since it is not that field's value.

**FR-10a** — Results are sorted by `ClimbCount`, descending. A single metro-area bbox returns ~180
crags; an LLM reading top-down should meet the significant ones first.

**FR-11** — An empty result set is a valid, successful response. A bbox covering open ocean returns
an empty list, not an error. This also covers the case where every upstream result was removed by
FR-9.

---

## 3. `get_area_details(areaId)`

Returns detail for a single area or crag.

**FR-12** — The tool accepts one argument, `areaId`, an OpenBeta area UUID.

**FR-13** — The response includes the area name, coordinates, ancestry path, grade system,
description, its climbs, and its child areas.

**FR-14** — *(Resolved — the TBD is closed.)* The type is `Area`, not `AreaType`, reached via the
root field `area(uuid: ID)`. Coordinates do follow the `metadata { lat lng }` pattern, as assumed.
The tool selects:

```graphql
area(uuid: $uuid) {
  uuid  areaName  totalClimbs  gradeContext  pathTokens
  metadata { lat lng leaf isBoulder }
  content { description }
  children { uuid areaName totalClimbs metadata { lat lng leaf } }
  climbs {
    uuid name fa length safety
    type   { sport trad bouldering alpine aid tr ice mixed snow deepwatersolo }
    grades { yds vscale font french uiaa ewbank wi }
  }
}
```

**FR-14a** — **Both `climbs` and `children` must be selected, because an area has one or the other,
never both.** OpenBeta stores climbs only on leaf areas; a parent returns `climbs: []` regardless of
its `totalClimbs`.

Stawamus Chief (`8f267065-fc1a-59ce-bcf1-6e9335548363`) reports `totalClimbs: 369` with an empty
`climbs` array and 32 children. A tool that read only `climbs` would report a 369-route wall as
having nothing on it. `children` is how a caller descends to the routes.

**FR-14b** — Discipline booleans are flattened to a string list, and the grade is chosen from the
system appropriate to the climb — V-scale/Font for boulders, YDS/French/Ewbank/UIAA/WI otherwise —
falling back to any populated system rather than reporting a graded climb as ungraded.

**FR-14c** — The API's in-band "not recorded" placeholders are mapped to zero values and omitted
from the output: `safety: "UNSPECIFIED"`, `fa: "unknown"`, and `length: -1`. Passed through, these
would have an LLM report a climb's first ascensionist as literally "unknown" and its length as -1
metres.

**FR-15** — As with FR-10, the tool returns a trimmed output type shaped for an LLM consumer, not
the raw GraphQL response: `AreaDetail`, holding `[]ClimbSummary` and `[]ChildArea`.

---

## 4. Error handling

**FR-16** — Upstream GraphQL errors surface to the client as MCP tool errors carrying the upstream
message. They must not be reported as empty successful results.

**FR-17** — Transport-level failures — connection refused, timeout, non-2xx HTTP status, unparseable
body — surface as MCP tool errors distinguishable from "query succeeded, no matches".

**FR-18** — Invalid input is rejected before any upstream call, with a message stating what was
wrong:

- `bbox` with a length other than 4
- `bbox` with `minLng > maxLng` or `minLat > maxLat`
- coordinates outside valid ranges (lng ±180, lat ±90)
- `areaId` that is not a well-formed UUID

---

## 5. Out of scope for this POC

Explicitly excluded. Each is a deliberate decision, with rationale in [plan.md](plan.md).

| Excluded | Reason |
| --- | --- |
| Caching layer (DynamoDB or otherwise) | Deferred until the upstream cost of `cragsWithin` is measured. Designing a cache key/TTL strategy around an unmeasured query is premature. |
| Hosted deployment / streamable-HTTP transport | Raised as an open question, not adopted. It was motivated mainly by a shared cache, which does not exist yet. |
| Per-client rate limiting, WAF rate-based rules, API keys | Only relevant to a hosted deployment. API-key throttling also cuts against a frictionless local install. |
| `search_climbs` | Present in RFC #487, dropped here — judged to be the wrong tool shape. |
| Point + radius proximity search | Out of scope for the two-tool POC — but see the correction below; the schema does support it. |

**Correction to [plan.md](plan.md):** plan.md states there is "no dedicated point/radius resolver
on the schema". Introspection shows otherwise — the root query field exists:

```graphql
cragsNear(placeId: String, lnglat: Point, minDistance: Int, maxDistance: Int, includeCrags: Boolean): [CragsNear]
input Point { lat: Float, lng: Float }
```

So a future "crags near me" tool would *not* need to synthesize a bbox client-side. This does not
change the POC scope — two tools, as specified — but it removes the technical objection recorded
against a third one.
