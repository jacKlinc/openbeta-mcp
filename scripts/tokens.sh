#!/usr/bin/env bash
# Usage: scripts/tokens.sh [extra args for tokens.sweep]
# Runs against the public API unless OPENBETA_ENDPOINT points elsewhere; the
# harness passes the environment through to the server subprocess. Against a
# local stack the pacing is pointless, so:
#
#   OPENBETA_ENDPOINT=http://localhost:4000 scripts/tokens.sh --delay 0
#
# Writes data/tokens/data.jsonl (token counts, from the harness) and
# data/tokens/roundtrips.jsonl (latency and HTTP round trips, from the server's
# own sink) — the same calls, under the same run id, joinable on args_sha — then
# pushes both to MLflow. Methodology, results and confounds are documented in
# data/tokens/README.md.
#
# Start the tracking server first:  uv run --project evals mlflow server --port 5000
# Turn the VPN off first — see the note in docs/retry.md.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

OUT_TOKENS=data/tokens/data.jsonl
OUT_ROUNDTRIPS=data/tokens/roundtrips.jsonl

echo "tokens: building openbeta-mcp"
go build -o openbeta-mcp ./cmd/openbeta-mcp

# Written outside the repository, then moved in: a run that dies halfway through
# leaves the tracked dataset as it was rather than half-appended.
TMP_TOKENS=$(mktemp -t tokens-XXXXXX.jsonl)
TMP_ROUNDTRIPS=$(mktemp -t tokens-rt-XXXXXX.jsonl)
trap 'rm -f "$TMP_TOKENS" "$TMP_ROUNDTRIPS"' EXIT

# OPENBETA_METRICS turns on the server's own sink for the same calls; the
# harness passes the environment through to the subprocess, so both datasets
# come out of one sweep. They land in data/tokens/ rather than being appended to
# the curated data/round-trip/data.jsonl: same schema, different query set.
export OPENBETA_METRICS="$TMP_ROUNDTRIPS"
export OPENBETA_RUN="tok-$(date -u +%Y%m%dT%H%M%SZ)"

echo "tokens: run $OPENBETA_RUN"

(cd evals && uv run python -m tokens.sweep --out "$TMP_TOKENS" "$@")

mkdir -p "$(dirname "$OUT_TOKENS")"
cp "$TMP_TOKENS" "$OUT_TOKENS"
[ -s "$TMP_ROUNDTRIPS" ] && cp "$TMP_ROUNDTRIPS" "$OUT_ROUNDTRIPS"
echo "tokens: $(wc -l <"$OUT_TOKENS") rows written to $OUT_TOKENS"

# Run from evals/ so the module resolves and picks up that project's venv; the
# dataset paths are made absolute first, since they are relative to the repo root.
# Never fatal: the datasets are on disk, and MLflow is a view over them.
ROOT=$(git rev-parse --show-toplevel)
(cd evals && uv run python -m common.export "$ROOT/$OUT_TOKENS" "$ROOT/$OUT_ROUNDTRIPS") || true
