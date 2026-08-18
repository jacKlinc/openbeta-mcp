# evals

Harness for measuring what the openbeta MCP server costs a model.

Every eval gets a folder. `common/` is the plumbing they share — the stdio
session, the commit stamp, JSONL reading and writing.

```
common/     session, provenance, dataset io
tokens/     what a tool result costs to read
roundtrip/  what a tool call costs in latency and HTTP round trips
judge/      next up
corpus/     the argument sets a sweep runs over, committed
```

Everything runs as a module from this directory, so the packages resolve:

```bash
uv run python -m tokens.sweep --limit 5     # measure, live
uv run python -m tokens.analysis ../data/tokens/data.jsonl
```

## Token sweep

```bash
go build -buildvcs=true -o openbeta-mcp ./cmd/openbeta-mcp   # the harness talks to this
scripts/tokens.sh                                            # from the repo root
```

`scripts/tokens.sh` refuses to run on a dirty tree, builds a stamped binary,
sweeps the whole corpus, and writes two datasets plus the plots. Results and
confounds are in [../data/tokens/README.md](../data/tokens/README.md).

A tool result is deterministic for its arguments, so calling one twice adds
nothing. **The spread comes from the breadth of the corpus, not from repetition**
— `n` is the number of argument sets, which is why `tokens/corpus.py` crosses
every gazetteer place with three search radii and crawls the area hierarchy
rather than looping over five fixed queries.

| Command | What it does |
|---|---|
| `python -m tokens.corpus` | print the corpus sizes |
| `python -m tokens.corpus --crawl` | rebuild `corpus/areas.json` from the live API |
| `python -m tokens.sweep` | measure every argument set, append rows, cache payloads |
| `python -m tokens.sweep --use-cache` | re-count from cached payloads, no live calls |
| `python -m tokens.schema` | size of the tool definitions, resent every turn |
| `python -m tokens.analysis <data.jsonl>` | table, plots, breaking point |
| `python -m roundtrip.analysis <data.jsonl>` | latency and round trips, `--by tool\|run\|commit\|args_sha` |

Payloads are cached under `data/tokens/cache/` by `args_sha`, so changing the
tokenizer or the analysis costs no upstream calls. The API is free and
volunteer-run: calls are serial and paced.

Rows carry the same field names the server's own sink writes, and `args_sha` is
computed exactly as Go computes it — same canonical JSON, same first 12 hex of
the SHA-256 — so the token dataset and the round-trip dataset join on
`(tool, args_sha)`.

## What the numbers are

`tiktoken`'s `o200k_base` is OpenAI's encoding, not Claude's, so **nothing here
is a Claude token count**. It undercounts Claude by roughly 15–20% on prose and
by more on JSON, which is exactly the shape of these tool results.

Treat every figure as a *relative* measure. Comparing two payloads, or watching
one grow across a change, is sound. Quoting one as "this call costs N tokens" is
not — and because the gap is wider on JSON than on prose, the schema-versus-output
balance shown here flatters the schemas.

## Why not real Claude counts

`anthropic.messages.count_tokens` is the authoritative count: the real model
tokenizer, server-side. It runs no inference and bills no tokens — but it still
goes through the developer API, and **a Claude Pro/Max subscription does not pay
for that**. On an unfunded account every call returns:

```
400 invalid_request_error: Your credit balance is too low to access the Anthropic API.
```

That path was built and removed. Bringing it back is `uv add anthropic` plus a
funded account; counts are model-specific, so pass the model the server is
actually evaluated against.

The free alternative is **`/context` in Claude Code**, which breaks the live
context window down by component — MCP tool schemas included — using the real
tokenizer, paid for by the subscription.

Claude Code transcripts under `~/.claude/projects/*/*.jsonl` also carry genuine
`usage` records (`cache_creation_input_tokens`, `cache_read_input_tokens`).
Deriving a tiktoken correction factor from consecutive-turn deltas was tried and
abandoned: across 600 pairs the implied ratio ranged from 1.63 to 2.39 chars per
token, because each delta also sweeps in system reminders and cache-boundary
effects. The `usage` numbers are real; that attribution is not.

## Two costs, billed differently

- **Tool output** is paid once, when the result comes back. That is what
  `tokens.sweep` measures.
- **Tool schemas** are paid on every single turn, whether or not the tool is
  called. That standing rent is why adding a tool is never free, and it is the
  measurement behind the tool-shape decision in
  [../docs/gazzetteer/README.md](../docs/gazzetteer/README.md) — one folded tool
  versus two separate ones. `tokens.schema` reports it.
