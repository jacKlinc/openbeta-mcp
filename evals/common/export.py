from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path

import mlflow
import pandas as pd

from common.config import get_settings
from common.jsonl import read_df

logger = logging.getLogger(__name__)

TRACKING_URI = "http://localhost:5000"
EXPERIMENT = "MCP Tool Performance"

# What the server was pointed at and how it was configured. Read from the
# environment at export time, which is only right because scripts/tokens.sh
# exports in the same shell as the sweep -- a re-export later would guess. The
# defaults mirror cmd/openbeta-mcp/main.go.

# Column -> metric prefix. A file's measurement is decided by the fields its rows
# carry, not its name, so one code path serves the token sweep and the Go bench.
KINDS = {
    "tokens": "tokens",
    "roundtrips": "http_roundtrips",
    "ms": "latency_ms",
}

PERCENTILES = (50, 95, 99)


def measures(df: pd.DataFrame) -> list[tuple[str, str]]:
    """The (column, metric prefix) pairs this frame can report."""
    return [(column, prefix) for column, prefix in KINDS.items() if column in df.columns]


def log_distribution(df: pd.DataFrame, column: str, prefix: str) -> None:
    """Per-call values as a rank-ordered series, plus the scalars that compare runs.

    Failed calls are excluded: a zero from a 502 is not a cheap call, and one in
    the series would put a false floor on the curve.
    """
    ok = df[~df["err"].fillna(False)] if "err" in df else df

    for tool, group in ok.groupby("tool"):
        values = group[column].sort_values().tolist()
        if not values:
            continue

        # step = rank, so MLflow's line chart draws the quantile curve.
        for rank, value in enumerate(values):
            mlflow.log_metric(f"{prefix}.{tool}", value, step=rank)

        series = pd.Series(values)
        scalars = {f"{prefix}.{tool}.n": len(values)}
        for pct in PERCENTILES:
            scalars[f"{prefix}.{tool}.p{pct}"] = series.quantile(pct / 100)
        mlflow.log_metrics(scalars)

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


def already_exported(run: str) -> bool:
    found = mlflow.search_runs(filter_string=f"tags.source_run_id = '{run}'", max_results=1)
    return not found.empty


def export(paths: list[Path], experiment: str, tracking_uri: str, force: bool) -> int:
    """Push every run found in the given JSONL datasets. Returns runs exported."""
    mlflow.set_tracking_uri(tracking_uri)
    mlflow.set_experiment(experiment)

    # Grouped by run rather than by file: one sweep writes token rows and
    # round-trip rows separately, and they belong in the same MLflow run.
    by_run: dict[str, list[pd.DataFrame]] = {}
    for path in paths:
        df, malformed = read_df(path)
        if malformed:
            logger.warning("%s: %d malformed lines skipped", path, malformed)
        if df.empty:
            continue
        for run, group in df.groupby("run"):
            by_run.setdefault(str(run), []).append(group)

    exported = 0
    for run, frames in by_run.items():
        if not force and already_exported(run):
            logger.info("%s already exported; --force to export it again", run)
            continue
        log_run(run, frames)
        exported += 1
        logger.info("logged %s (%d rows)", run, sum(len(f) for f in frames))

    return exported


def main() -> int:
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    parser = argparse.ArgumentParser(description="Push measurement datasets to MLflow.")
    parser.add_argument("paths", type=Path, nargs="+", help="JSONL datasets to export")
    parser.add_argument("--experiment", default=EXPERIMENT)
    parser.add_argument("--tracking-uri", default=TRACKING_URI)
    parser.add_argument("--force", action="store_true", help="export runs already present")
    args = parser.parse_args()

    paths = [p for p in args.paths if p.exists()]
    if not paths:
        logger.warning("nothing to export")
        return 0

    # The JSONL is already on disk by the time this runs, so a tracking server
    # that is down or moved costs a view of the data, never the data.
    try:
        export(paths, args.experiment, args.tracking_uri, args.force)
    except Exception as exc:
        logger.warning("mlflow export failed (%s); datasets are still on disk", exc)
        return 0

    return 0


if __name__ == "__main__":
    sys.exit(main())
