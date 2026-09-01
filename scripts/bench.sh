#!/usr/bin/env bash
# Run the round-trip benchmarks against the live API and refresh the dataset.
#
# Usage: scripts/bench.sh [output.jsonl]
#
# Defaults to data/round-trip/data.jsonl. Methodology, results and confounds are
# documented in data/round-trip/README.md.
#
# Turn the VPN off first — see the note in docs/retry.md.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

OUT=${1:-data/round-trip/data.jsonl}

# Per-tool sample sizes. The two fan-out tools cost 1 + min(crags in radius, 20)
# round trips per call against a free, volunteer-run API, which is what caps
# them well below the 50 used for the single-query tool.
AREA_N=50
CRAGS_N=20
CLIMBS_N=20

# Written outside the repository, then moved in: a run that dies halfway through
# leaves the tracked dataset as it was rather than half-appended.
TMP=$(mktemp -t roundtrip-XXXXXX.jsonl)
trap 'rm -f "$TMP"' EXIT

export OPENBETA_LIVE=1
export OPENBETA_METRICS="$TMP"
export OPENBETA_RUN="exp-$(date -u +%Y%m%dT%H%M%SZ)"

echo "bench: run $OPENBETA_RUN"

# One invocation per tool: -benchtime applies to every benchmark it matches, so
# a single `-bench .` run would give the fan-out tools the area tool's sample
# size. Each benchmark caps its own iterations and fails rather than overrunning.
run() {
	local bench=$1 n=$2
	echo "bench: $bench n=$n"
	go test -tags bench -run '^$' \
		-bench "$bench" -benchtime="${n}x" -timeout 900s ./internal/mcpserver
}

run BenchmarkGetAreaDetails "$AREA_N"
run BenchmarkCragsNear "$CRAGS_N"
run BenchmarkFindClimbs "$CLIMBS_N"

mkdir -p "$(dirname "$OUT")"
cp "$TMP" "$OUT"
echo "bench: $(wc -l <"$OUT") samples written to $OUT"

# Run from evals/ so the module resolves and picks up that project's venv; the
# dataset path is made absolute first, since $OUT is relative to the repo root.
# Never fatal: the dataset is on disk, and MLflow is a view over it.
OUT_ABS=$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")
(cd evals && uv run python -m tokens.export "$OUT_ABS") || true
