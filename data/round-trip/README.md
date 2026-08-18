# Round-trip measurements

Measured. 93 samples from commit `124ef4b`, Go 1.26.0, one machine, 2026-08-16.

How many HTTP round trips each MCP tool costs, and how long a call takes. The
question behind it: `crags_near` and `find_climbs` fan out one upstream query per
crag, and nothing recorded what that actually costs.

`data.jsonl` is the dataset. Every number below is derived from it and can be
recomputed from the run in MLflow — see [Analysis](#analysis).

## What a sample is

One MCP tool call, recorded by the receiving middleware in
[server.go](../../internal/mcpserver/server.go) and written by
[metrics.go](../../internal/mcpserver/metrics.go). Counting happens in
`openbeta.CountTransport`, which sees HTTP round trips; recording happens in the
MCP layer, which is the only place that knows what a tool call is.

| Field        | Meaning                                                                            |
| ------------ | ---------------------------------------------------------------------------------- |
| `v`          | Schema version. `0` — unreleased, still free to change.                            |
| `run`        | Groups samples from one process, so an interrupted run is separable.               |
| `ts`         | Call start, UTC.                                                                   |
| `tool`       | Tool name.                                                                         |
| `args`       | The arguments, verbatim, so the exact query can be re-run.                         |
| `args_sha`   | 12 hex chars of the canonicalised args; groups by query without string-matching.   |
| `roundtrips` | HTTP round trips the call made. Includes redirects and, once retry lands, retries. |
| `ms`         | Wall clock, fractional — sub-millisecond rejections truncated to `0` as integers.  |
| `err`        | Tool failed. Covers `IsError` results, not only transport errors.                  |

The samples in this file also carry `commit`, `dirty` and `go`, recorded from the
binary's VCS stamp. They are **v0 fields and no longer written**: MLflow tags each
exported run with the commit it was exported from, so the server stamping its own
provenance was a second source of truth to keep in step. `v` tells the two shapes
apart.

## Cost model

| Tool               | Round trips                               |
| ------------------ | ----------------------------------------- |
| `get_area_details` | 1                                         |
| `crags_near`       | 1 + min(crags in radius, `MaxCrags` = 20) |
| `find_climbs`      | 1 + min(crags in radius, `MaxCrags` = 20) |

Both fan-out tools share `nearestCrags` ([find_climbs.go](../../internal/tools/find_climbs.go))
and `fetchAreaDetails` ([crags_near.go](../../internal/tools/crags_near.go)): one
query to find crags, then one per crag. The fan-out exists because `cragsNear`
returns empty `climbs` and `children`, and `totalClimbs` is 0 on most leaf crags
— see [graphql-findings.md](../../docs/graphql-findings.md) §1 and §4.

## Results

| Tool               | n   | mean ms | p50   | p95    | max    | mean rt | max rt | fails |
| ------------------ | --- | ------- | ----- | ------ | ------ | ------- | ------ | ----- |
| `get_area_details` | 51  | 179.6   | 141.8 | 432.2  | 737.6  | 1.00    | 1      | 0     |
| `crags_near`       | 21  | 455.1   | 109.8 | 1604.1 | 1769.3 | 8.24    | 21     | 0     |
| `find_climbs`      | 21  | 401.3   | 134.1 | 1129.3 | 1383.6 | 8.24    | 21     | 0     |

397 upstream requests in total. No failures, so the transient Cloudflare shapes
in [retry.md](../../docs/retry.md) did not appear in this window.

**Read the fan-out percentiles with care.** Round trips are not distributed
around 8.24 — they are trimodal, because the 5 km radius produces a different
number of crags at each origin:

| Origin          | Round trips                      | n   |
| --------------- | -------------------------------- | --- |
| Squamish        | 21 (hits the `MaxCrags` ceiling) | 10  |
| Red River Gorge | 14                               | 8   |
| Bishop          | 1                                | 8   |
| Fontainebleau   | 1                                | 8   |
| Kalymnos        | 1                                | 8   |

Three of the five origins have **no crags within 5 km** of their gazetteer point,
so those calls never fan out at all. The `crags_near` p95 of 1604 ms is the
Squamish mode, not a tail of a single distribution. `get_area_details` is the
only tool here whose percentiles describe one population.

## Sample sizes

| Tool               | n   | radius | queries               | worst case requests |
| ------------------ | --- | ------ | --------------------- | ------------------- |
| `get_area_details` | 51  | —      | 5 area UUIDs          | 50                  |
| `crags_near`       | 21  | 5 km   | 5 places              | 420                 |
| `find_climbs`      | 21  | 5 km   | 5 places + grade band | 420                 |

n is one higher than the `-benchtime=Nx` value because the testing framework
probes each benchmark with `b.N=1` before the measured run, and that probe is a
real call that gets recorded. The extra sample is always query index 0.

n=20 is short of the n=50 target for the fan-out tools, deliberately: at 21 round
trips per call the API budget goes before the sample size does. At that n a p95
sits between the top two observations, which is why the script warns below n=20
and why `roundtrips` — machine-independent and near-deterministic per query — is
the number to publish rather than `ms`.

## How to reproduce

Start the tracking server first, since the run is pushed to it when the
benchmark finishes:

```
uv run --project evals mlflow server --port 5000
```

Samples are written **outside the repository** and moved in at the end, so a run
that dies halfway leaves the tracked dataset as it was.

```
scripts/bench.sh                    # refreshes data/round-trip/data.jsonl
scripts/bench.sh /tmp/scratch.jsonl # somewhere else
```

[scripts/bench.sh](../../scripts/bench.sh) enforces both rules above, holds the per-tool sample
sizes, and prints the summary when it finishes. Turn the VPN off first —
[retry.md](../../docs/retry.md) explains why a VPN reads as upstream flakiness here.

Three separate invocations, because `-benchtime` applies to every benchmark it
matches — `-bench . -benchtime=50x` would give each fan-out tool 50 calls. Each
benchmark carries a hard iteration cap that fails on a run that omits
`-benchtime=Nx` entirely, where the default 1s ramp would put over 200,000
requests through a fan-out tool.

Squash and rebase merging are disabled on the repository. Both rewrite commits,
which would leave the commit tagged on every exported MLflow run pointing at
nothing.

## Query set

Origins, all resolved by the compiled-in [gazetteer](../../internal/geo/gazetteer.go)
so the lookup itself costs no round trips: `Squamish`, `Bishop`,
`Red River Gorge`, `Fontainebleau`, `Kalymnos` — three continents, so one
regional slowdown cannot define the latency distribution. `maxDistanceKm: 5`.
`find_climbs` adds `minGrade: 5.8`, `maxGrade: 5.11a`; grade filtering happens
after the crags are fetched, so it changes results but not round trips.

Areas, spanning the response shapes the tool returns:

| UUID                                   | Area                | Shape        |
| -------------------------------------- | ------------------- | ------------ |
| `8f267065-fc1a-59ce-bcf1-6e9335548363` | Stawamus Chief      | 32 sub-areas |
| `fbe1956f-65c2-5515-a26f-127bf15fe598` | Grand Wall Boulders | 201 climbs   |
| `7f74ea62-664e-581e-a929-f01f6bf68f37` | Apron Boulders      | 55 climbs    |
| `17a692c8-9e34-5511-90e7-44ef23d10fa1` | The Apron           | 51 climbs    |
| `e0d61bef-a560-5b18-88ea-7068dabc2bb2` | Olesen Creek Wall   | 8 climbs     |

Rediscover the children with `get_area_details` on the Chief.

## Analysis

The dataset is pushed to MLflow, which is where the numbers are read:

```
uv run --project evals python -m common.export data/round-trip/data.jsonl
```

`scripts/bench.sh` runs this itself when it finishes, so this is only needed for a
backfill or after the tracking server has been rebuilt. Exporting is idempotent —
a run already present is skipped unless `--force` is given.

In the UI, each tool's per-call values are a metric series ordered by rank, which
reads as a quantile curve: `ms.<tool>` and `http_roundtrips.<tool>`. The p50/p95/p99
scalars are what compare one run against another. `charts.json` in the run's
artifacts declares the intended charts, since MLflow keeps chart layouts in
browser-side state rather than on the server.

## Confounds

- **No retry.** [retry.md](../../docs/retry.md) is unimplemented, so the
  transient failures it catalogues land as `err: true` samples with truncated
  round-trip counts. Adding retry will raise `roundtrips` for identical queries;
  samples are only comparable within a retry regime, which is what `commit` is
  for.
- **Partial fan-out failures are invisible in the result.** `fetchAreaDetails`
  drops per-crag failures and errors only when all fail, so a call can succeed
  with fewer round trips than the crag count implies. Do not infer crags-in-radius
  from `roundtrips`.
- **`detailConcurrency = 5`** bounds wall clock, not round trips. `ms` is roughly
  `ceil(N/5)` upstream latencies, so a concurrency change moves `ms` and leaves
  `roundtrips` flat.
- **Upstream is a moving target.** Crag counts are user-contributed; the same
  query can cross the `MaxCrags` ceiling later. `ts` bounds this, and the query
  set pins the input.
- **Network position.** `ms` includes this machine's link to `api.openbeta.io`.
  Compare `ms` only across commits measured from the same machine.
- **In-memory transport.** The benchmark drives the server over
  `mcp.NewInMemoryTransports`, so stdio framing is excluded from `ms` —
  sub-millisecond against 100–1800 ms upstream, but a known omission.
