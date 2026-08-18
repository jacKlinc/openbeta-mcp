import mlflow

mlflow.set_tracking_uri("http://localhost:5000")
mlflow.set_experiment("MCP Eval")

with mlflow.start_run(run_name="find_climbs-test"):
    event = {
        "v": 0,
        "run": "tok-3700e95",
        "ts": "2026-08-18T15:46:42.843970152Z",
        "tool": "find_climbs",
        "args": {
            "place": "The Gunks",
            "maxDistanceKm": 20,
            "minGrade": "5.8",
            "maxGrade": "5.11a",
        },
        "args_sha": "051833f57404",
        "roundtrips": 21,
        "ms": 779.502,
        "err": False,
        "commit": "3700e952de8d54ee34f5ace901869b4e121ecb0a",
        "dirty": False,
        "go": "go1.26.0",
    }

    mlflow.log_metric("http_roundtrips", event["roundtrips"])
    mlflow.log_metric("latency_ms", event["ms"])

    mlflow.set_tag("tool", event["tool"])
    mlflow.set_tag("git_commit", event["commit"])
    mlflow.set_tag("go_version", event["go"])

    mlflow.log_dict(event, "raw-event.json")


print("done")
