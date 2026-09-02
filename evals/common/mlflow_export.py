"""The plumbing every MLflow export shares.

Two exports exist — per-call cost and per-case judge runs — and they differ only
in how a row becomes metrics. Everything around that is identical: connect,
group by run, skip what is already there, and never let a tracking server that
is down fail a job whose data is already on disk.
"""

from __future__ import annotations

import argparse
import logging
from collections.abc import Callable, Iterator
from pathlib import Path

import mlflow
import pandas as pd
from mlflow.exceptions import MlflowException

from common.jsonl import read_df

logger = logging.getLogger(__name__)

TRACKING_URI = "http://localhost:5000"

# Rank-ordered series plus these, which is what compares two runs.
PERCENTILES = (50, 95, 99)

# Takes a run id and its rows, and logs one MLflow run.
LogRun = Callable[[str, list[pd.DataFrame]], None]


def log_series(values: list[float], name: str) -> None:
    """A sorted series logged against rank, so MLflow draws the quantile curve."""
    if not values:
        return

    series = pd.Series(sorted(values))
    for rank, value in enumerate(series):
        mlflow.log_metric(name, float(value), step=rank)

    mlflow.log_metrics(
        {f"{name}.n": len(series), f"{name}.mean": float(series.mean())}
        | {f"{name}.p{pct}": float(series.quantile(pct / 100)) for pct in PERCENTILES}
    )


def already_exported(run: str) -> bool:
    found = mlflow.search_runs(filter_string=f"tags.source_run_id = '{run}'", max_results=1)
    return not found.empty


def frames_by_run(paths: list[Path], key: str) -> Iterator[tuple[str, list[pd.DataFrame]]]:
    """Rows from every path, grouped by run id.

    Grouped across files rather than per file: one cost sweep writes token rows
    and round-trip rows separately, and they belong in the same MLflow run.
    """
    grouped: dict[str, list[pd.DataFrame]] = {}
    for path in paths:
        df, malformed = read_df(path)
        if malformed:
            logger.warning("%s: %d malformed lines skipped", path, malformed)
        if df.empty:
            continue
        for run, group in df.groupby(key):
            grouped.setdefault(str(run), []).append(group)

    yield from grouped.items()


def export(paths: list[Path], experiment: str, force: bool, key: str, log_run: LogRun) -> int:
    """Push every run in the given datasets. Returns how many were exported."""
    mlflow.set_tracking_uri(TRACKING_URI)
    mlflow.set_experiment(experiment)

    exported = 0
    for run, frames in frames_by_run(paths, key):
        if not force and already_exported(run):
            logger.info("%s already exported; --force to export it again", run)
            continue
        log_run(run, frames)
        exported += 1
        logger.info("logged %s (%d rows)", run, sum(len(f) for f in frames))

    return exported


def main(description: str, experiment: str, key: str, log_run: LogRun) -> int:
    """The CLI both exports share. Never fails a job over a missing view."""
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    parser = argparse.ArgumentParser(description=description)
    parser.add_argument("paths", type=Path, nargs="+", help="JSONL datasets to export")
    parser.add_argument("--force", action="store_true", help="export runs already present")
    args = parser.parse_args()

    paths = [p for p in args.paths if p.exists()]
    if not paths:
        logger.warning("nothing to export")
        return 0

    # A tracking server that is down or moved costs a view of the data, never the
    # data. Narrow on purpose: a bug here should raise, not read as an outage.
    try:
        export(paths, experiment, args.force, key, log_run)
    except (MlflowException, OSError) as exc:
        logger.warning("mlflow export failed (%s); datasets are still on disk", exc)

    return 0
