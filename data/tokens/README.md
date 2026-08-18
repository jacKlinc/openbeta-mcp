# Token measurements

Measured. 357 calls from commit `3700e95`, one machine, 2026-08-18.

What a tool result costs a model to read, as a distribution rather than a single
number. The question behind it: nothing recorded how large these payloads get,
so "we should paginate" had no number attached to it.

`data.jsonl` is the dataset and `roundtrips.jsonl` the HTTP cost of the same
calls. Every number below is derived from them and can be recomputed from the run
in MLflow — see [Analysis](#analysis).

## Results

| Tool               | n   | p50   | p90   | p95   | p99   | max   | mean  |
| ------------------ | --- | ----- | ----- | ----- | ----- | ----- | ----- |
| `crags_near`       | 144 | 118   | 1,922 | 1,997 | 2,282 | 2,282 | 842   |
| `find_climbs`      | 144 | 40    | 3,542 | 3,745 | 3,773 | 3,773 | 968   |
| `get_area_details` | 69  | 852   | 2,630 | 3,615 | 6,659 | 6,688 | 1,301 |

**The two fan-out tools are bimodal, and their percentiles describe two
populations rather than a tail.** For `crags_near`, 78 of 144 calls return
effectively nothing — a median of 31 tokens, the JSON envelope around an empty
crag list — while the other 66 sit at a median of 1,791. There is almost nothing
in between. The p50 of 118 is not a typical call; it is an artefact of where the
empty half ends. `find_climbs` is the same shape, one step further: the grade
filter empties more results still, so its median is 40 tokens and its populated
mode is roughly twice as expensive as `crags_near` on the same origins.

`get_area_details` is the only tool here whose distribution is continuous, which
is why it is also the only one with a meaningful tail: the p99 of 6,659 is over
seven times its median. It is also uncapped — the fan-out tools truncate at
`MaxCrags = 20` and `MaxClimbs = 30`, so their maxima are ceilings the code
imposes, while `get_area_details` returns whatever the area holds.

**The ceiling, not the radius, is what bounds fan-out cost.** Results are sorted
by distance before truncation, so once a radius holds 20 crags, widening it
returns the same nearest 20. 36 of 48 places produce byte-identical payloads at
5, 20 and 50 km. The radius only matters for the sparse origins, where it decides
whether anything comes back at all: Bishop goes from 32 tokens at 5 km to 1,902
at 20 km, and stays there at 50.

### Against the round-trip cost

The same calls, joined on `args_sha` with `roundtrips.jsonl`:

| Tool               | median round trips | tokens per upstream request |
| ------------------ | ------------------ | --------------------------- |
| `crags_near`       | 3                  | 82                          |
| `find_climbs`      | 3                  | 94                          |
| `get_area_details` | 1                  | 1,301                       |

3,041 upstream requests produced 357 results. The fan-out tools spend an order of
magnitude more upstream work per token delivered, because most of what they fetch
is discarded by the distance sort and the cap.

Across the whole dataset, 2.77 characters per token — well under the ~4 that
prose averages, which is the JSON penalty the caveat below refers to.

## What a row is

One MCP tool call, measured by the Python harness in
[evals/tokens/sweep.py](../../evals/tokens/sweep.py). The harness launches the
compiled binary and speaks MCP over stdio, the same as a real client, and counts
the text blocks of the result — what the model actually reads.

| Field      | Meaning                                                                          |
| ---------- | -------------------------------------------------------------------------------- |
| `v`        | Schema version. `1` since the commit stamp came out; see below.                  |
| `run`      | Groups rows from one sweep. `tok-<UTC timestamp>`, shared with `roundtrips.jsonl`. |
| `ts`       | Call time, UTC.                                                                  |
| `tool`     | Tool name.                                                                       |
| `args`     | The arguments, verbatim, so the exact query can be re-run.                       |
| `args_sha` | 12 hex chars of the canonicalised args, computed exactly as the Go side does.    |
| `tokens`   | Tokens in the result's text blocks under `encoding`. `0` on a failed call.       |
| `chars`    | Characters in the same text, so the tokens-per-char ratio stays checkable.       |
| `err`      | Tool failed. Covers `isError` results and transport failures alike.              |
| `encoding` | The tokenizer. `o200k_base` — OpenAI's, not Claude's. See the caveat below.      |

The rows committed here are **v0**, and carry `commit` and `dirty` as well. Those
fields are no longer written: MLflow tags each exported run with the commit it was
exported from, so recording provenance here too was a second source of truth to
keep in step. `v` tells the two shapes apart, and the exporter reads both.

`roundtrips.jsonl` sits beside it: the same calls, recorded by the server's own
sink, in the schema documented in [../round-trip/README.md](../round-trip/README.md).
Both carry the same `run`, and `args_sha` is computed identically on both sides,
so the two datasets join on `(tool, args_sha)` — token cost against HTTP cost for
the same query.

## The caveat that governs every number here

`o200k_base` is OpenAI's encoding. **Nothing here is a Claude token count** — it
undercounts Claude by roughly 15–20% on prose and by more on JSON, which is what
these payloads are. Every figure is a *relative* measure: comparing two payloads,
or watching one grow across a commit, is sound; quoting one as "this call costs N
tokens" is not.

## Corpus

A tool result is deterministic for its arguments, so repeating a call adds no
spread. **`n` is the number of distinct argument sets, not an iteration count** —
the distribution is only as honest as the corpus is wide.

| Tool               | Arguments                                                     |
| ------------------ | ------------------------------------------------------------- |
| `crags_near`       | every gazetteer place × `maxDistanceKm` ∈ {5, 20, 50}         |
| `find_climbs`      | the same, plus `minGrade: 5.8`, `maxGrade: 5.11a`             |
| `get_area_details` | UUIDs from a breadth-first crawl, seeded at Stawamus Chief    |

n is 144, 144 and 69. The area arm is short of its 150 target because the crawl
exhausted the Squamish subtree — every UUID reachable from the Chief, and no path
out of it, since `get_area_details` exposes children but not parents or siblings.
Widening it means seeding the crawl from `crags_near` results at other origins.

Places come from the compiled-in [gazetteer](../../internal/geo/gazetteer.go),
parsed from the Go source so the harness cannot invent a name the server fails to
resolve. The area list is committed at
[evals/corpus/areas.json](../../evals/corpus/areas.json) and rebuilt with
`python -m tokens.corpus --crawl`.

## How to reproduce

Start the tracking server first, since the sweep pushes to it when it finishes:

```
uv run --project evals mlflow server --port 5000
```

```
scripts/tokens.sh                    # full sweep, live
scripts/tokens.sh --limit 5          # smoke run, 5 argument sets per tool
scripts/tokens.sh --use-cache        # re-count from cached payloads, no API calls
```

Payloads are cached under `data/tokens/cache/` (gitignored) by `args_sha`, so
re-counting after a tokenizer change costs nothing upstream. Turn the VPN off
first — [retry.md](../../docs/retry.md) explains why.

## Analysis

Both datasets are pushed to MLflow under one run, keyed by the `run` field:

```
uv run --project evals python -m common.export data/tokens/data.jsonl data/tokens/roundtrips.jsonl
```

`scripts/tokens.sh` runs this itself when it finishes, so this is only needed for a
backfill or after the tracking server has been rebuilt. Exporting is idempotent —
a run already present is skipped unless `--force` is given.

In the UI, each tool's per-call values form a metric series ordered by rank
(`tokens.<tool>`, `latency_ms.<tool>`, `http_roundtrips.<tool>`), which reads as a
quantile curve: a flat shelf is a mode, the rise at the right is the tail. The
bimodality described above is the long shelf at ~31 tokens. The p50/p95/p99 scalars
are what compare one run against another, and `charts.json` in the run's artifacts
declares the intended charts — MLflow keeps chart layouts in browser-side state, so
the spec is what makes the view reproducible.

## Confounds

- **The tokenizer is not Claude's.** Stated above, repeated here because it is
  the confound that invalidates absolute numbers.
- **`structuredContent` is not counted.** The Go SDK emits it alongside the text
  blocks. A client that sends both pays roughly twice what these rows say.
- **Tool schemas are not in this dataset.** They are billed every turn rather
  than once per call, so they are measured separately by `python -m tokens.schema`.
- **The gazetteer is not a sample of real queries.** It is the set of places the
  server can resolve, weighted by nothing. Read the distribution as the shape of
  what the tools *can* return, not of what a user will ask for.
- **Upstream is a moving target.** Crag and climb counts are user-contributed and
  grow. `ts` bounds each sweep and the corpus pins the inputs; the commit is
  tagged on the MLflow run.
- **One call failed on an upstream 502** — `find_climbs` at Fair Head, the
  transient Cloudflare shape catalogued in [retry.md](../../docs/retry.md). It is
  recorded with `err: true` and `tokens: 0`, and drags that tool's mean down
  slightly; percentiles are unaffected at this n. Retry is unimplemented, so this
  is the raw failure rate — an earlier sweep of the same corpus hit two.
- **An empty result is not free.** The ~31-token floor is the JSON envelope and
  the resolved origin, which every call pays whether or not it finds anything.
