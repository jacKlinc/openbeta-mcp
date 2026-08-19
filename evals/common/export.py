from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path

import mlflow
import pandas as pd

from common.jsonl import read_df

logger = logging.getLogger(__name__)

TRACKING_URI = "http://localhost:5000"
EXPERIMENT = "MCP Tool Performance"

# The charts worth looking at, declared here and logged with every run.
#
# MLflow keeps the layouts a person builds in the UI in browser-side view state,
# so a view does not travel between machines and nothing server-side reads a
# config file. This makes the intended view part of the run: reviewable in a
# diff, and enough to rebuild the charts in the chart builder.
#
# The distribution charts are the ones that replaced the ECDF. Each per-call
# metric is logged with step = its rank among that tool's values sorted
# ascending, so the built-in line chart draws the quantile curve — the ECDF with
# its axes transposed.
#
# Two limits of the UI shape what can be declared here. A series takes its
# colour from its run rather than its metric, so one run's three tools arrive in
# one colour told apart only by dash pattern. And a bar chart plots a single
# metric with bars as runs, so a tail comparison across tools has to be parallel
# coordinates instead. Histograms and cost-against-an-argument have no form at
# all in MLflow; those are in common/plots.py.
CHARTS = {
    "version": 1,
    "charts": [
        {
            "title": "Token cost distribution",
            "type": "line",
            "metrics": ["tokens.crags_near", "tokens.find_climbs", "tokens.get_area_details"],
            "x": "step (rank, ascending)",
            "y": "tokens per call",
            "y_scale": "log",
            "note": "Reads as a quantile curve: a flat shelf is a mode, the rise at the right is the tail.",
        },
        {
            "title": "Latency distribution",
            "type": "line",
            "metrics": ["latency_ms.crags_near", "latency_ms.find_climbs", "latency_ms.get_area_details"],
            "x": "step (rank, ascending)",
            "y": "ms",
            "y_scale": "log",
        },
        {
            "title": "Upstream requests per call",
            "type": "line",
            "metrics": [
                "http_roundtrips.crags_near",
                "http_roundtrips.find_climbs",
                "http_roundtrips.get_area_details",
            ],
            "x": "step (rank, ascending)",
            "y": "HTTP round trips",
        },
        {
            "title": "Tail by tool, across runs",
            "type": "parallel coordinates",
            "metrics": ["tokens.<tool>.p50", "tokens.<tool>.p95", "tokens.<tool>.p99"],
            "note": (
                "Compare runs, not calls: one line per run across the percentile axes. "
                "<tool> is each of the three tool names."
            ),
        },
    ],
}

# Which measurement a file holds, decided by the fields its rows carry rather
# than by its name — one code path for the token sweep, the Go bench, and any
# backfill of an older dataset.
KINDS = {
    "tokens": ("tokens", "tokens"),
    "roundtrips": ("roundtrips", "http_roundtrips"),
    "ms": ("ms", "latency_ms"),
}

PERCENTILES = (50, 95, 99)


def measures(df: pd.DataFrame) -> list[tuple[str, str]]:
    """The (column, metric prefix) pairs this frame can report."""
    return [pair for column, pair in KINDS.items() if column in df.columns]


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

        for rank, value in enumerate(values):
            mlflow.log_metric(f"{prefix}.{tool}", value, step=rank)

        # Percentiles and a count, nothing else. MLflow draws one chart card per
        # metric name, so every extra scalar is another flat bar to scroll past --
        # and mean, max and sum are all recoverable from the JSONL a run came from.
        # The mean is the one worth losing: these distributions are bimodal, so it
        # lands in the empty gap between modes and describes no call that happened.
        series = pd.Series(values)
        scalars = {f"{prefix}.{tool}.n": len(values)}
        for pct in PERCENTILES:
            scalars[f"{prefix}.{tool}.p{pct}"] = series.quantile(pct / 100)
        mlflow.log_metrics(scalars)

    mlflow.log_metric(f"{prefix}.total", ok[column].sum())
    # Per measurement rather than per run: one failed call appears in both the
    # token and the round-trip dataset, and a combined count would report it twice.
    mlflow.log_metric(f"{prefix}.fails", int(len(df) - len(ok)))


def log_run(run: str, frames: list[pd.DataFrame]) -> None:
    """One MLflow run for one sweep, across however many datasets it produced."""
    combined = pd.concat(frames, ignore_index=True)

    with mlflow.start_run(run_name=run):
        mlflow.set_tag("source_run_id", run)
        mlflow.log_params(
            {
                "rows": len(combined),
                "tools": combined["tool"].nunique(),
                "queries": combined["args_sha"].nunique(),
                "encoding": combined.get("encoding", pd.Series(["-"])).iloc[0],
            }
        )

        for frame in frames:
            for column, prefix in measures(frame):
                log_distribution(frame, column, prefix)
            mlflow.log_table(frame, f"raw/{measures(frame)[0][1]}_events.json")

        mlflow.log_dict(CHARTS, "charts.json")


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
