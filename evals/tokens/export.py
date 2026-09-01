"""Push per-call cost datasets to MLflow: the token sweep and the Go bench.

Judge runs are per-case with nested token counts, and live in judge/export.py.
The plumbing both share is in common/mlflow_export.py.
"""

from __future__ import annotations

import sys

import mlflow
import pandas as pd

from common.config import get_settings
from common.mlflow_export import log_series, main

EXPERIMENT = "MCP Tool Performance"

# One sweep writes token rows and round-trip rows separately, under one run id.
RUN_KEY = "run"

# Column -> metric prefix. A file's measurement is decided by the fields its rows
# carry, not its name, so one code path serves the token sweep and the Go bench.
KINDS = {
    "tokens": "tokens",
    "roundtrips": "http_roundtrips",
    "ms": "latency_ms",
}


def measures(df: pd.DataFrame) -> list[tuple[str, str]]:
    """The (column, metric prefix) pairs this frame can report."""
    return [(column, prefix) for column, prefix in KINDS.items() if column in df.columns]


def log_distribution(df: pd.DataFrame, column: str, prefix: str) -> None:
    """One series per tool, plus the totals across them.

    Failed calls are excluded: a zero from a 502 is not a cheap call, and one in
    the series would put a false floor on the curve.
    """
    ok = df[~df["err"].fillna(False)] if "err" in df else df

    for tool, group in ok.groupby("tool"):
        log_series(group[column].tolist(), f"{prefix}.{tool}")

    mlflow.log_metric(f"{prefix}.total", ok[column].sum())
    # Per measurement: one failed call lands in both datasets and would count twice.
    mlflow.log_metric(f"{prefix}.fails", int(len(df) - len(ok)))


def log_run(run: str, frames: list[pd.DataFrame]) -> None:
    """One MLflow run for one sweep, across however many datasets it produced."""
    combined = pd.concat(frames, ignore_index=True)
    settings = get_settings()

    with mlflow.start_run(run_name=run):
        mlflow.set_tag("source_run_id", run)
        mlflow.log_params(
            {
                "rows": len(combined),
                "tools": combined["tool"].nunique(),
                "queries": combined["args_sha"].nunique(),
                "encoding": combined.get("encoding", pd.Series(["-"])).iloc[0],
                "endpoint": settings.openbeta_endpoint,
                "max_crags": settings.openbeta_max_crags,
            }
        )

        for frame in frames:
            for column, prefix in measures(frame):
                log_distribution(frame, column, prefix)
            mlflow.log_table(frame, f"raw/{measures(frame)[0][1]}_events.json")


if __name__ == "__main__":
    sys.exit(main("Push measurement datasets to MLflow.", EXPERIMENT, RUN_KEY, log_run))
