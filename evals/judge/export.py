"""Push judge runs to MLflow: per-case behaviour, not per-call cost."""

from __future__ import annotations

import sys

import mlflow
import pandas as pd

from common.jsonl import read_df
from common.mlflow_export import log_series, main
from judge.dataset import GRADES

EXPERIMENT = "MCP Judge Runs"

RUN_KEY = "run_id"

# Column -> metric prefix. response_ms is whole-loop wall time including tool
# round trips, deliberately not called latency: tokens/export.py measures
# per-call latency, and the two would invite a meaningless comparison.
KINDS = {
    "tokens": "tokens",
    "usage.input_tokens": "tokens_in",
    "usage.output_tokens": "tokens_out",
    "tool_call_count": "tool_calls",
    "ms": "response_ms",
}


def flatten(df: pd.DataFrame) -> pd.DataFrame:
    """Lift the nested usage counts to columns and derive the per-case totals."""
    usage = pd.json_normalize(df["usage"]).add_prefix("usage.")
    flat = pd.concat([df.drop(columns=["usage"]).reset_index(drop=True), usage], axis=1)

    flat["tokens"] = flat["usage.input_tokens"] + flat["usage.output_tokens"]
    flat["tool_call_count"] = flat["tool_calls"].apply(len)
    flat["failed"] = flat["error"].notna()
    return flat


# Fields that describe the run rather than a case, so every row must agree. Taking
# row zero without checking would silently label a mixed run by whichever came first.
RUN_PARAMS = [
    "model",
    "provider",
    "tools_enabled",
    "endpoint",
    "max_crags",
    "harness_version",
    "tool_server_sha",
]


def run_params(flat: pd.DataFrame) -> dict[str, object]:
    """The run-level fields, checked to be constant across its rows."""
    mixed = {c: sorted(map(str, flat[c].unique())) for c in RUN_PARAMS if flat[c].nunique() > 1}
    if mixed:
        raise ValueError(f"rows disagree on run-level fields: {mixed}")
    return {c: flat[c].iloc[0] for c in RUN_PARAMS}


def log_grades(run: str, cases: int) -> None:
    """Quality metrics for one run, if it has been graded.

    Precision gates and recall diagnoses: a trimmed variant that drops routes and
    one that invents them are different failures, and F1 alone cannot separate them.
    """
    if not GRADES.exists():
        return
    grades, _ = read_df(GRADES)
    graded = grades[(grades["run_id"] == run) & grades["graded"]] if not grades.empty else grades
    if graded.empty:
        return

    # Logged so nobody reads a p99 off six values.
    mlflow.log_param("graded_cases", len(graded))
    mlflow.log_metric("pass_rate", float(graded["passed"].mean()))
    mlflow.log_metric("ungraded_cases", cases - len(graded))

    for column in ("precision", "recall", "f1"):
        log_series(graded[column].dropna().tolist(), column)

    # happy_path and honest_failure fail for different reasons; one mean hides that.
    for category, group in graded.groupby("category"):
        mlflow.log_metric(f"pass_rate.by_category.{category}", float(group["passed"].mean()))


def log_run(run: str, frames: list[pd.DataFrame]) -> None:
    """One MLflow run per sweep, tagged so a tools run and its baseline compare."""
    flat = flatten(pd.concat(frames, ignore_index=True))
    ok = flat[~flat["failed"]]
    params = run_params(flat)

    with mlflow.start_run(run_name=run):
        mlflow.set_tag("source_run_id", run)
        # The axis every comparison turns on: same cases, tools on versus off.
        mlflow.set_tag("tools_enabled", str(bool(params["tools_enabled"])))
        mlflow.set_tag("model", params["model"])

        mlflow.log_params(
            params
            | {
                "cases": flat["case_id"].nunique(),
                "attempts": int(flat["attempt"].max()),
                "rows": len(flat),
            }
        )

        for column, prefix in KINDS.items():
            log_series(ok[column].tolist(), prefix)
            mlflow.log_metric(f"{prefix}.total", float(ok[column].sum()))

        log_grades(run, flat["case_id"].nunique())

        mlflow.log_metric("cases.failed", int(flat["failed"].sum()))
        # Tools available and none called: correct for a clarification case,
        # a finding for any other.
        mlflow.log_metric("cases.no_tool_call", int((flat["tools_enabled"] & (flat["tool_call_count"] == 0)).sum()))

        for category, group in flat.groupby("category"):
            mlflow.log_metric(f"tokens.by_category.{category}", float(group["tokens"].mean()))

        # Answers and tool calls are the point of a judge run, so the table
        # carries them; the series above are only the summary.
        mlflow.log_table(
            flat[
                [
                    "case_id",
                    "capability",
                    "category",
                    "tokens",
                    "tool_call_count",
                    "turns",
                    "ms",
                    "stop_reason",
                    "answer",
                ]
            ],
            "raw/judge_cases.json",
        )


if __name__ == "__main__":
    sys.exit(main("Push judge runs to MLflow.", EXPERIMENT, RUN_KEY, log_run))
