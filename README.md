# openbeta-mcp

A Go MCP server over the [OpenBeta](https://openbeta.io) climbing database, exposing rock climbing
areas and routes to LLM clients. Read-only, single binary, stdio transport.

Third-party proof of concept — not affiliated with OpenBeta, and not a replacement for the official
TypeScript server proposed in [RFC #487](https://github.com/OpenBeta/openbeta-graphql/issues/487).

> Climbing information is user-contributed and can be wrong. Grades, protection and access notes
> are opinions, not facts. Verify anything safety-critical against a current local guidebook.

## Status

The GraphQL query layer is built and tested against the live API. The MCP server wiring — tool
registration and stdio transport — is not written yet, so there is nothing to install.

| Piece                                  | State       |
| -------------------------------------- | ----------- |
| `cragsWithin` query + filtering        | done        |
| `area` query + mapping                 | done        |
| MCP tool registration, stdio transport | not started |

## Tools (planned)

**`crags_within(bbox, zoom)`** — crags inside a bounding box.

`bbox` is `[minLng, minLat, maxLng, maxLat]` — **longitude first**. `zoom` selects the level of the
area hierarchy: 11 or above returns individual crags, below that returns parent regions.

**`get_area_details(areaId)`** — name, coordinates, description, routes and sub-areas for one area.

Areas hold either routes or sub-areas, never both. A large area returns its children; descend
through them to reach the routes.

## Development

```bash
go build ./...
go test ./...          # offline, no network
```

Tests against the live API are opt-in, so the default run is deterministic:

```bash
OPENBETA_LIVE=1 go test -run Live -v ./internal/openbeta
```

CI runs fmt, vet, build and the offline tests on every push. The live tests run nightly (02:00 PST)
rather than per-push — they exist to catch upstream schema drift, and the API is a free service run
by volunteers.

## Layout

```
internal/openbeta/    GraphQL client, queries, wire and output types
docs/                 requirements and verified API findings
plan.md               design rationale
```

## Documentation

- [Functional requirements](docs/functional-requirements.md) — what the server does
- [Non-functional requirements](docs/non-functional-requirements.md) — how it should behave
- [GraphQL findings](docs/graphql-findings.md) — what the live API actually returns, and where it
  is surprising

That last document is worth reading before touching the query layer. `Area.totalClimbs` is not a
climb count and filtering on it discards most real crags; `zoom` changes which level of the
hierarchy you get back. Both are load-bearing and neither is obvious from the schema.

## License

MIT. Climbing data belongs to OpenBeta and its contributors, under their own terms.
