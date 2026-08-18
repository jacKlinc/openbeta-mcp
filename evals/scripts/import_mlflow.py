import json
from collections import defaultdict
from pathlib import Path

import mlflow
import pandas as pd

TRACKING_URI = "http://localhost:5000"
EXPERIMENT = "MCP Tool Performance"

TOKENS_FILE = Path("data/tokens/data.jsonl")
ROUNDTRIPS_FILE = Path("data/tokens/roundtrips.jsonl")


def read_jsonl(path: Path) -> list[dict]:
    with path.open() as f:
        return [json.loads(line) for line in f if line.strip()]


def main():
    mlflow.set_tracking_uri(TRACKING_URI)
    mlflow.set_experiment(EXPERIMENT)

    token_events = read_jsonl(TOKENS_FILE)
    roundtrip_events = read_jsonl(ROUNDTRIPS_FILE)

    # Group all observations by evaluation/run ID.
    runs = defaultdict(lambda: {"tokens": [], "roundtrips": []})

    for event in token_events:
        runs[event["run"]]["tokens"].append(event)

    for event in roundtrip_events:
        runs[event["run"]]["roundtrips"].append(event)

    for run_id, data in runs.items():
        tokens = data["tokens"]
        roundtrips = data["roundtrips"]

        # Collect common metadata.
        all_events = tokens + roundtrips

        commit = next((e["commit"] for e in all_events if "commit" in e), None)
        dirty = next((e["dirty"] for e in all_events if "dirty" in e), None)

        with mlflow.start_run(run_name=run_id):
            # ---- Metadata ----

            mlflow.set_tags(
                {
                    "source_run_id": run_id,
                    "git_commit": commit or "unknown",
                    "git_dirty": str(dirty),
                }
            )

            # ---- Aggregate metrics ----

            successful_tokens = [e for e in tokens if not e["err"]]
            successful_roundtrips = [e for e in roundtrips if not e["err"]]

            if successful_tokens:
                mlflow.log_metric(
                    "tokens_total",
                    sum(e["tokens"] for e in successful_tokens),
                )

            if successful_roundtrips:
                latencies = [e["ms"] for e in successful_roundtrips]
                http_calls = [e["roundtrips"] for e in successful_roundtrips]

                mlflow.log_metric("latency_ms", sum(latencies))
                mlflow.log_metric("http_roundtrips", sum(http_calls))

            mlflow.log_metric("token_events", len(tokens))
            mlflow.log_metric("roundtrip_events", len(roundtrips))

            # ---- Per-tool metrics ----

            for event in successful_tokens:
                tool = event["tool"]
                mlflow.log_metric(f"tokens.{tool}", event["tokens"])

            for event in successful_roundtrips:
                tool = event["tool"]
                mlflow.log_metric(f"latency_ms.{tool}", event["ms"])

                mlflow.log_metric(f"http_roundtrips.{tool}", event["roundtrips"])

            # ---- Raw data ----

            mlflow.log_table(pd.DataFrame(tokens), "raw/token_events.json")

            mlflow.log_table(pd.DataFrame(roundtrips), "raw/roundtrip_events.json")

        print(f"logged {run_id}")


if __name__ == "__main__":
    main()
