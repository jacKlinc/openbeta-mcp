# Charts

Two viewers over the same JSONL. MLflow compares runs; a notebook shows what one
run's distribution actually looks like.

| Question                                  | Where                                          |
| ----------------------------------------- | ---------------------------------------------- |
| Did this change move the tail?            | MLflow, across runs                            |
| What does the spread look like?           | MLflow quantile curve, or a notebook histogram |
| Where is the mode, how heavy is the tail? | notebook — `histograms`                        |
| Does cost track an argument?              | notebook — `by_distance`                       |
| Is `n` big enough to quote a p99?         | notebook — `small_groups`                      |

MLflow logs no per-call dimension for anything inside a row's `args`, and has no
histogram over a metric series. Those two shapes only exist in the notebook.

## From a notebook

Start Jupyter from `evals/`, and keep the notebook here too — the kernel puts
its own directory on the path and nothing else finds `common`:

```bash
uv run --group dev jupyter lab
```

```python
from pathlib import Path
from common.jsonl import read_df
from common.plots import ecdf, histograms, by_distance, summarise, small_groups

df, _ = read_df(Path("../data/tokens/data.jsonl"))
summarise(df, "tool")          # n, mean, p50/p90/p95/p99, max, fails
ecdf(df)                       # returns a Figure; renders inline
histograms(df)["crags_near"]
by_distance(df)
```

Every function returns a Figure and writes nothing: `fig.savefig(...)` if you
want a file. Bind it — `fig = ecdf(df)` — if you would rather not see the figure
twice; the inline backend draws it at the end of the cell, and the returned
Figure's repr draws it again.

Pass `column=` for the other measures — `ms` and `roundtrips` from
`../data/round-trip/data.jsonl` or `../data/tokens/roundtrips.jsonl`:

```python
rt, _ = read_df(Path("../data/round-trip/data.jsonl"))
ecdf(rt, "ms")
ecdf(rt, "roundtrips")         # linear axis; MEASURES turns log off for counts
```

Each tool keeps a fixed colour across every figure, so filtering one out never
repaints the others.

## In MLflow

http://localhost:5000 → **MCP Tool Performance** → tick a run → **Chart** view →
**Add chart**. Layouts live in browser-side state, so nothing built in the UI
travels; `CHARTS` in [../common/export.py](../common/export.py) is the copy that
does, shipped to every run as `charts.json`.

What a run gives you, per tool, per measure — `tokens`, `latency_ms`,
`http_roundtrips`:

| Metric                               | Shape                                                                 |
| ------------------------------------ | --------------------------------------------------------------------- |
| `<measure>.<tool>`                   | one point per call, `step` = rank among that tool's values, ascending |
| `<measure>.<tool>.n`                 | calls in the run                                                      |
| `<measure>.<tool>.p50` `.p95` `.p99` | the scalars that compare runs                                         |
| `<measure>.total`, `<measure>.fails` | run-level                                                             |

**Quantile curve.** Line chart over `tokens.crags_near`, `tokens.find_climbs`,
`tokens.get_area_details`, x-axis **Step**, y-axis log. This is the ECDF with its
axes transposed — rank along x instead of share up y — so it reads the same way
rotated: a flat shelf is a mode, the rise at the right is the tail, the knee is
the breaking point. Read a percentile at the matching fraction of x. Log y is
not optional; the payloads span two orders of magnitude and a linear axis
flattens everything below the tail into the floor.

Series colour follows the *run*, not the metric, so one run's three tools come
out one colour separated by dash pattern. The legend is the only reliable
identity here — the notebook figures are the ones with a colour per tool.

**Tails across runs.** Parallel coordinates over `tokens.<tool>.p50` / `.p95` /
`.p99`, with two or more runs ticked: one line per run across the percentile
axes, and a change that widens a tail shows as p99 climbing while p50 holds
still. Bar charts take a single metric with bars as runs, so they give you one
percentile at a time rather than the comparison.

Swap the prefix for `latency_ms` or `http_roundtrips`. An
`http_roundtrips.<tool>.p99` above 1 means a call fanned out upstream.

## Small n

A p99 over fewer than 20 calls describes one unlucky call, not a tail. MLflow
will chart it without complaint, so check `<measure>.<tool>.n` before quoting a
percentile from a `--limit`ed sweep — or call `small_groups(summarise(df))`,
which lists exactly the groups that fail the threshold.
