# Non-Functional Requirements — OpenBeta MCP Server (POC)

Qualities the server must have, as distinct from the behaviour specified in
[functional-requirements.md](functional-requirements.md). Rationale lives in [plan.md](plan.md).

Requirement IDs are stable once assigned.

---

## 1. Success criterion

**NFR-1** — The success signal for this POC is **correctness**: real queries return the right data.
It is explicitly *not* latency and *not* cache hit rate.

This governs how every requirement below is weighted. Where correctness and performance conflict,
correctness wins, and no requirement here should be read as licence to trade it away.

---

## 2. Performance posture

**NFR-2** — Server-side overhead is expected to be dominated by upstream GraphQL latency. This is an
accepted characteristic of an I/O-bound proxy, not a target to optimize against.

Measured against the live API: single-area lookups return in ~120–150ms; `cragsWithin` over a metro
bbox returns in ~130ms without climb counts and ~1.2s with them. The tenfold difference is the price
of FR-9's correctness fix, and NFR-1 says that is the right trade.

**NFR-3** — No latency SLO is defined for this POC. Deliberate: the performance case for the
language choice was assessed as weak for this workload, and inventing a target would contradict
that assessment and misdirect effort away from NFR-1.

**NFR-4** — Every upstream HTTP call carries a timeout and honours the incoming `context.Context`,
so a hung upstream cannot wedge the server indefinitely.

---

## 3. Distribution and runtime

**NFR-5** — The server ships as a single statically-linked binary with no runtime dependencies — in
particular, no Node.js installation required. This is a primary motivation for the Go
implementation.

**NFR-6** — Installation is: obtain the binary, point an MCP client at it. No package manager, no
runtime version management, no build step for the end user.

---

## 4. Dependencies

**NFR-7** — The GraphQL client is `net/http` plus hand-written request/response structs per query.
No codegen client (e.g. genqlient): the setup cost — schema file, build step — is not justified by
two hand-written queries.

Revisit trigger: the query surface grows meaningfully beyond the two tools.

**NFR-8** — Third-party dependencies are limited to the MCP Go SDK. Everything else comes from the
standard library.

---

## 5. Architecture

**NFR-9** — Tool and handler logic must not depend on the transport. Handlers take parsed arguments
and return typed results; the transport is a thin outer layer at startup.

Consequence: moving from `mcp.StdioTransport` to a streamable-HTTP transport is a change at the
composition root, not a fork of the logic. Hosting is out of scope (see the functional requirements),
but this requirement is what keeps that option cheap, so it holds regardless.

**NFR-10** — GraphQL wire structs are separate types from tool output structs. The wire structs
track the upstream schema; the output structs track what the LLM should see. They are allowed to
diverge, and a schema change must not silently reshape tool output.

---

## 6. LLM-facing output

**NFR-11** — Tool responses are trimmed summary types, never raw upstream payloads. Two reasons:
token economy, and signal-to-noise for the consuming model — fewer irrelevant fields means fewer
things for the model to latch onto.

**NFR-12** — Tool names, descriptions, and argument schemas are written for an LLM caller. Anything
easy to get wrong — notably the `bbox` element ordering — is stated in the description rather than
left implicit in the type.

---

## 7. Upstream contract

**NFR-13** — The server targets the public API at `https://api.openbeta.io/graphql`. Read-only, no
authentication, no credentials to configure or store.

The `/graphql` path is required. The bare origin accepts simple queries but returns a non-JSON
`error code: 502` page for larger ones, so it is not a usable endpoint.

**NFR-14** — No private or negotiated rate limit is assumed. The server must behave acceptably as an
ordinary anonymous consumer of a public endpoint, and must not retry aggressively on failure.

**NFR-15** — The upstream schema is treated as external and subject to change. Verified findings
(field names, the `SearchWithinFilter` shape, bbox ordering) are recorded with the fact that they
were confirmed against the live API, so a future break is traceable to a schema change rather than
mistaken for a bug in this server.

---

## 8. Positioning

**NFR-16** — This is a third-party proof of concept. It is not proposed as a replacement for the
official TypeScript/AGPL package described in RFC #487, and documentation must not present it as
one.

---

## 9. Testability

**NFR-17** — Every functional requirement is verifiable, either against the live API or against a
recorded fixture. A requirement that cannot be checked either way should be rewritten or dropped.

**NFR-18** — Handler logic — argument validation, the climb-count filter of FR-9, wire-to-summary
mapping — is testable without network access, which follows from the transport and type separation
in NFR-9 and NFR-10.
