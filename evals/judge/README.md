# judge

Does the MCP server help a model answer climbing questions? Cost is measured
separately in [`evals/tokens/`](../tokens/); this is the quality half.

## The pipeline

```
golden-set.jsonl ──▶ groundtruth.py ──▶ expected values + manifest fingerprints
                                              │
                     runner.py ──▶ model ⇄ MCP tools ──▶ runs.jsonl
                                              │
                                       export.py ──▶ MLflow
```

Three commands:

```
python -m judge.groundtruth          # derive expected values from the local snapshot
python -m judge.groundtruth --check  # verify nothing drifted
python -m judge.runner               # run the set against the model
python -m judge.runner --no-tools    # the baseline the server has to beat
```

The runner exports to MLflow on its own; `python -m judge.export <path>` re-runs
that alone.

## Modules

| | |
| --- | --- |
| `models.py` | The case schema. Import this, not `groundtruth`, if you only need `Case`. |
| `payload.py` | Reading a tool response: what the expected value is, what fingerprints it. |
| `groundtruth.py` | The generate/check CLI. |
| `runner.py` | The agent loop. Records what happened; grades nothing. |
| `export.py` | Result rows to MLflow, over the plumbing in `common/mlflow_export.py`. |
| `data/` | The set itself — see [data/README.md](data/README.md). |

## Grading

**Not built yet.** `runner.py` captures answers and the exact tool output the model
saw, because that is what a grader needs; nothing scores them. Per
[docs/plans/judge.md](../../docs/plans/judge.md) the order is: deterministic
graders for the 8 computable cases, hand-label the 19 prose ones, then build the
judge and measure agreement against those labels.

The three tiers, from [design.md](../docs/design.md), in order of preference:

| `expected.kind` | Graded by | Cases |
| --- | --- | --- |
| `scalar` | Exact match | 2 |
| `set` | Set F1, precision and recall reported separately | 6 |
| `prose` | Judge, on groundedness | 19 |

Precision and recall stay separate because they are different bugs: dropping
routes is a recall failure, inventing them a precision failure, and averaging
hides which is happening.

The judge sees the question, the tool output the model had, and the answer — not
the expected value. It checks entailment against the context, not correctness
against the world.

## Running it

Needs the local stack (`scripts/dev-up.sh`) and, for `runner.py` only, an
`ANTHROPIC_API_KEY`. See [`.template.env`](../../.template.env). Settings live in
[`common/config.py`](../common/config.py).
