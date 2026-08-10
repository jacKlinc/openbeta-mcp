# openbeta-mcp

A Go MCP server over the [OpenBeta](https://openbeta.io) climbing database, exposing rock climbing
areas and routes to LLM clients. Read-only, single binary, stdio transport.

> [!NOTE]
> **AI Transparency Disclosure:** This project utilized AI coding assistants to generate the POC


Third-party proof of concept — not affiliated with OpenBeta, and not a replacement for the official
TypeScript server proposed in [RFC #487](https://github.com/OpenBeta/openbeta-graphql/issues/487).

> Climbing information is user-contributed and can be wrong. Grades, protection and access notes
> are opinions, not facts. Verify anything safety-critical against a current local guidebook.


## Download GraphQL Schema

### Download

```bash
npx get-graphql-schema https://api.openbeta.io
npx get-graphql-schema https://api.openbeta.io > schema/openbeta.graphql
```

### Generate a typed client with `genqlient`

[genqlient.yaml](genqlient.yaml) points at the vendored schema, reads every operation in
[internal/openbeta/queries/](internal/openbeta/queries/) and writes a typed Go client to
[internal/openbeta/generated/](internal/openbeta/generated/). It generates code for *your queries*,
type-checked against the schema — not a binding for the whole API.

Write a query — one operation per named block, and the operation name becomes the generated
function name. [internal/openbeta/queries/area.graphql](internal/openbeta/queries/area.graphql):

```graphql
query GetArea($uuid: ID!) {
  area(uuid: $uuid) {
    uuid
    areaName
    pathTokens
    children {
      uuid
      areaName
    }
  }
}
```

Regenerate after any change to the schema or the queries. The generator version is pinned in
[tools.go](tools.go) so this is reproducible:

```bash
go generate ./internal/openbeta/
```

The result is committed — generation is not part of the build. Then call it;
[cmd/genq](cmd/genq/) is a hello-world runner:

```bash
go run ./cmd/genq
# Stawamus Chief ([Canada British Columbia Squamish Stawamus Chief])
#   - The Bulletheads bb83077a-9c1a-5f8c-aaf1-7a5fdebd5c0b
#   ...
```

Two things bite early: the root area field is `area(uuid:)`, not `area(id:)`, and an unknown uuid
comes back as a GraphQL error (`area Area ... not found`) rather than a null.

## Install

**Add as a package:**

```bash
go install github.com/jacKlinc/openbeta-mcp/cmd/openbeta-mcp@latest
```

**Fork and build:**
```bash
gh repo clone https://github.com/jacKlinc/openbeta-mcp
cd openbeta-mcp
go build -o openbeta-mcp ./cmd/openbeta-mcp
```

That produces a single binary with no runtime dependencies. There is no API key and nothing to
configure — the OpenBeta API is public and read-only. `-endpoint` overrides the GraphQL URL and
`-version` prints the build version; neither is needed for normal use.

Point an MCP client at the binary using an **absolute path** — clients don't necessarily run with
the working directory you'd expect.

### Claude Code

```bash
claude mcp add openbeta -- "$(pwd)/openbeta-mcp"
```

Verify it connected:

```bash
claude mcp list             # connection status for every configured server
claude mcp get openbeta     # config and failure detail for this one
claude mcp remove openbeta  # undo
```

`/mcp` inside a session shows the same status plus the tool list.

Then ask for something that needs it — *"what can I climb near Squamish?"* — and Claude Code should
call `crags_within`.


## Tools

**`crags_within(bbox, zoom)`** — crags inside a bounding box, largest first.

`bbox` is `[minLng, minLat, maxLng, maxLat]` — **longitude first**. `zoom` is optional and selects
the level of the area hierarchy: 11 or above returns individual crags, below that returns parent
regions. It defaults to 13.

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
cmd/openbeta-mcp/            binary: flags, stdio transport, shutdown
cmd/genq/                    scratch runner for the generated client
internal/mcpserver/          MCP tool definitions and handlers
internal/openbeta/           GraphQL client, queries, wire and output types
internal/openbeta/queries/   .graphql operations, input to genqlient
internal/openbeta/generated/ genqlient output, committed
schema/openbeta.graphql      vendored upstream schema
tools.go                     pins the generator in the module graph
docs/                        requirements and verified API findings
docs/plan.md                 design rationale
```

The generated client is under `internal/` so it stays an implementation detail rather than public
API surface, and the queries sit next to the package that consumes them.

The tool logic doesn't depend on the transport — [internal/mcpserver](internal/mcpserver/) returns a
configured server and [cmd/openbeta-mcp](cmd/openbeta-mcp/) decides how to run it, so adding an HTTP
transport later is a change at the composition root rather than a fork of the handlers.

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
