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

# The commit stamped into every sample has to describe the code that ran, and Go
# derives that from `git status --porcelain` with no -uno — so untracked files
# count as dirty just as modified ones do. Checked here to fail in one obvious
# place rather than three benchmarks deep.
if [ -n "$(git status --porcelain)" ]; then
	echo "bench: working tree is dirty; commit or stash first, untracked files included" >&2
	git status --short >&2
	exit 1
fi

# Written outside the repository, then moved in. Appending straight to a tracked
# data.jsonl would dirty the tree, and the next invocation would compile — and
# stamp — dirty.
TMP=$(mktemp -t roundtrip-XXXXXX.jsonl)
trap 'rm -f "$TMP"' EXIT

export OPENBETA_LIVE=1
export OPENBETA_METRICS="$TMP"
export OPENBETA_RUN="exp-$(git rev-parse --short HEAD)"

echo "bench: commit $(git rev-parse --short HEAD), run $OPENBETA_RUN"

# One invocation per tool: -benchtime applies to every benchmark it matches, so
# a single `-bench .` run would give the fan-out tools the area tool's sample
# size. Each benchmark caps its own iterations and fails rather than overrunning.
# -buildvcs=true is required — `go test` builds a test-only package, which the
# default -buildvcs=auto leaves unstamped, and the harness refuses to record
# samples it cannot attribute to a commit.
run() {
	local bench=$1 n=$2
	echo "bench: $bench n=$n"
	go test -tags bench -buildvcs=true -run '^$' \
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
OUT_ABS=$(cd "$(dirname "$OUT")" && pwd)/$(basename "$OUT")
(cd evals && uv run python -m roundtrip.analysis "$OUT_ABS") || true
