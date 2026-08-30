"""Push judge runs to MLflow: per-case behaviour, not per-call cost.

Separate from common/export.py because a judge row is a different shape — one
per case, with token counts nested under usage — so KINDS would find only `ms`
and silently drop everything that matters. The plumbing both share is in
common/mlflow_export.py.

No pass rate yet, because no grader exists. Report tokens per *successful* case
once success can be measured; until then a variant that fails fast looks cheap.

    python -m judge.export data/judge/runs.jsonl
"""

from __future__ import annotations

import sys

import mlflow
import pandas as pd

from common.mlflow_export import log_series, main

EXPERIMENT = "MCP Judge Runs"

RUN_KEY = "run_id"

# Column -> metric prefix. response_ms is whole-loop wall time including tool
# round trips, deliberately not called latency: common/export.py measures
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


def log_run(run: str, frames: list[pd.DataFrame]) -> None:
    """One MLflow run per sweep, tagged so a tools run and its baseline compare."""
    flat = flatten(pd.concat(frames, ignore_index=True))
    ok = flat[~flat["failed"]]
    first = flat.iloc[0]

    with mlflow.start_run(run_name=run):
        mlflow.set_tag("source_run_id", run)
        # The axis every comparison turns on: same cases, tools on versus off.
        mlflow.set_tag("tools_enabled", str(bool(first["tools_enabled"])))
        mlflow.set_tag("model", first["model"])

        mlflow.log_params(
            {
                "model": first["model"],
                "provider": first["provider"],
                "tools_enabled": bool(first["tools_enabled"]),
                "cases": flat["case_id"].nunique(),
                "attempts": int(flat["attempt"].max()),
                "rows": len(flat),
                "endpoint": first["endpoint"],
                "max_crags": int(first["max_crags"]),
                "harness_version": first["harness_version"],
                "tool_server_sha": first["tool_server_sha"],
            }
        )

        for column, prefix in KINDS.items():
            log_series(ok[column].tolist(), prefix)
            mlflow.log_metric(f"{prefix}.total", float(ok[column].sum()))

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
