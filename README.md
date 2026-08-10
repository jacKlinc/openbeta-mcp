# openbeta-mcp

A Go MCP server over the [OpenBeta](https://openbeta.io) climbing database, exposing rock climbing
areas and routes to LLM clients. Read-only, single binary, stdio transport.

> [!NOTE]
> **AI Transparency Disclosure:** This project utilized AI coding assistants to generate the POC


Third-party proof of concept — not affiliated with OpenBeta, and not a replacement for the official
TypeScript server proposed in [RFC #487](https://github.com/OpenBeta/openbeta-graphql/issues/487).

> Climbing information is user-contributed and can be wrong. Grades, protection and access notes
> are opinions, not facts. Verify anything safety-critical against a current local guidebook.

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
cmd/openbeta-mcp/     binary: flags, stdio transport, shutdown
internal/mcpserver/   MCP tool definitions and handlers
internal/openbeta/    GraphQL client, queries, wire and output types
docs/                 requirements and verified API findings
docs/plan.md          design rationale
```

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
