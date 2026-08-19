import os

# MLflow reads these when it is imported, and its defaults are a 120 second
# timeout with five retries and backoff — so a tracking server that is simply not
# running holds a finished sweep for minutes before giving up. Exporting is
# optional, since the datasets are already on disk by then, so it should fail in
# seconds instead. Set here rather than in common.export because this package is
# imported first, and setdefault so a slow or remote server can override both.
os.environ.setdefault("MLFLOW_HTTP_REQUEST_TIMEOUT", "10")
os.environ.setdefault("MLFLOW_HTTP_REQUEST_MAX_RETRIES", "1")
