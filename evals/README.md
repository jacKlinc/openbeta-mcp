# evals

Two halves: [`tokens/`](tokens/) measures what tool responses cost, and
[`judge/`](judge/README.md) measures whether they help a model answer.

Harness for measuring what the openbeta MCP server costs a model.

Every eval gets a folder. `common/` is the plumbing they share — the stdio
session, the tokenizer, dataset io, and the MLflow export.

```
common/     session, tokenizer, dataset io, export
tokens/     what a tool result costs to read
judge/      next up
corpus/     the argument sets a sweep runs over, committed
```

**JSONL is the source of truth; MLflow is a view over it.** Every sweep writes its
dataset to disk first and exports second, so a run survives the tracking server
being down, moved or rebuilt — and the datasets stay readable by anything that can
read a line of JSON.

Start the tracking server before a sweep:

```bash
uv run mlflow server --port 5000
```

Everything else runs as a module from this directory, so the packages resolve:

```bash
uv run python -m tokens.sweep --limit 5     # measure, live
uv run python -m common.export ../data/tokens/data.jsonl
```

## Token sweep

```bash
go build -o openbeta-mcp ./cmd/openbeta-mcp   # the harness talks to this
scripts/tokens.sh                             # from the repo root
```

`scripts/tokens.sh` builds the binary, sweeps the whole corpus, writes both
datasets, and pushes them to MLflow. Results and confounds are in
[../data/tokens/README.md](../data/tokens/README.md).

A tool result is deterministic for its arguments, so calling one twice adds
nothing. **The spread comes from the breadth of the corpus, not from repetition**
— `n` is the number of argument sets.

Breadth is not the same as volume, though. `tokens/corpus.py` walks each place
up a radius ladder and keeps only the rungs whose payloads differ, because above
the `MaxCrags` cap a wider radius returns the same nearest crags and a second
call there is a byte-identical duplicate. The gazetteer-crossed-with-radii
corpus this replaced spent 144 calls per tool to buy 32 calls' worth of
information, 60 of them on empty results. See
[../docs/findings/corpus/README.md](../docs/findings/corpus/README.md).

| Command | What it does |
|---|---|
| `python -m tokens.corpus` | print the corpus sizes |
| `python -m tokens.corpus --probe` | rebuild `corpus/origins.json`: walk each place up the radius ladder |
| `python -m tokens.corpus --crawl` | rebuild `corpus/areas.json` from the area hierarchy |
| `python -m tokens.sweep` | measure every argument set, append rows, cache payloads |
| `python -m tokens.sweep --use-cache` | re-count from cached payloads, no live calls |
| `python -m tokens.schema` | size of the tool definitions, resent every turn |
| `python -m common.export <data.jsonl>...` | push runs to MLflow; `--force` re-exports |

Payloads are cached under `data/tokens/cache/` by `args_sha`, so changing the
tokenizer costs no upstream calls. The public API is free and volunteer-run:
calls are serial and paced. Against a local stack neither applies —
`OPENBETA_ENDPOINT=http://localhost:4000 scripts/tokens.sh --delay 0` finishes
in seconds.

Two things the cache does not know about: `args_sha` covers the arguments only,
so a payload cached under one endpoint or one `OPENBETA_MAX_CRAGS` is
indistinguishable from another. A live sweep always overwrites, so only
`--use-cache` can read a payload from a run it did not mean. The same blind spot
applies to the `endpoint` and `max_crags` MLflow params: they are read from the
environment at export time, which is right when `scripts/tokens.sh` exports in
the same shell as the sweep, and a guess if you re-export a dataset later.

Rows carry the same field names the server's own sink writes, and `args_sha` is
computed exactly as Go computes it — same canonical JSON, same first 12 hex of
the SHA-256 — so the token dataset and the round-trip dataset join on
`(tool, args_sha)` and export as one MLflow run.

## Reading a run

Per-call values are logged as a metric series ordered by rank, so MLflow's own
line chart draws the quantile curve — a flat shelf is a mode, the rise at the
right is the tail. That is the ECDF, transposed. The p50/p95/p99 scalars are
what compare runs.

MLflow keeps chart layouts in browser-side state, so a view built in the UI does
not travel; [docs/charts.md](docs/charts.md) is the record of which metrics form
which chart.

The shapes MLflow has no form for — a histogram, cost against an argument — are
figures in [common/plots.py](common/plots.py), written to be imported from a
notebook. [docs/charts.md](docs/charts.md) covers both viewers and which one
answers which question.

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
