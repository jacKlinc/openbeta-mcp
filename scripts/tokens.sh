#!/usr/bin/env bash
# Sweep the token corpus against the live API and refresh the dataset.
#
# Usage: scripts/tokens.sh [-- extra args for tokens.sweep]
#
# Writes data/tokens/data.jsonl (token counts, from the harness) and
# data/tokens/roundtrips.jsonl (latency and HTTP round trips, from the server's
# own sink) — the same calls, under the same run id, joinable on args_sha.
# Methodology, results and confounds are documented in data/tokens/README.md.
#
# Turn the VPN off first — see the note in docs/retry.md.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

OUT_TOKENS=data/tokens/data.jsonl
OUT_ROUNDTRIPS=data/tokens/roundtrips.jsonl

# Every row is attributed to a commit, and Go derives that from `git status
# --porcelain` with no -uno — so untracked files count as dirty just as modified
# ones do. Checked here to fail in one obvious place rather than mid-sweep.
#
# OPENBETA_TOKENS_ALLOW_DIRTY=1 lifts the gate for a smoke run, mirroring
# OPENBETA_BENCH_ALLOW_DIRTY on the Go side. Rows written that way carry
# dirty: true and the analysis drops them unless asked for.
if [ -z "${OPENBETA_TOKENS_ALLOW_DIRTY:-}" ] && [ -n "$(git status --porcelain)" ]; then
	echo "tokens: working tree is dirty; commit or stash first, untracked files included" >&2
	git status --short >&2
	exit 1
fi

# -buildvcs=true is required: the harness speaks to this binary over stdio, and
# an unstamped binary records samples it cannot attribute to a commit.
echo "tokens: building openbeta-mcp"
go build -buildvcs=true -o openbeta-mcp ./cmd/openbeta-mcp

# Written outside the repository, then moved in. Appending straight to a tracked
# data.jsonl would dirty the tree, and the next invocation would compile — and
# stamp — dirty.
TMP_TOKENS=$(mktemp -t tokens-XXXXXX.jsonl)
TMP_ROUNDTRIPS=$(mktemp -t tokens-rt-XXXXXX.jsonl)
trap 'rm -f "$TMP_TOKENS" "$TMP_ROUNDTRIPS"' EXIT

# OPENBETA_METRICS turns on the server's own sink for the same calls; the
# harness passes the environment through to the subprocess, so both datasets
# come out of one sweep. They land in data/tokens/ rather than being appended to
# the curated data/round-trip/data.jsonl: same schema, different query set.
export OPENBETA_METRICS="$TMP_ROUNDTRIPS"
export OPENBETA_RUN="tok-$(git rev-parse --short HEAD)"

echo "tokens: commit $(git rev-parse --short HEAD), run $OPENBETA_RUN"

(cd evals && uv run python -m tokens.sweep --out "$TMP_TOKENS" "$@")

mkdir -p "$(dirname "$OUT_TOKENS")"
cp "$TMP_TOKENS" "$OUT_TOKENS"
[ -s "$TMP_ROUNDTRIPS" ] && cp "$TMP_ROUNDTRIPS" "$OUT_ROUNDTRIPS"
echo "tokens: $(wc -l <"$OUT_TOKENS") rows written to $OUT_TOKENS"

# Run from evals/ so the module resolves and picks up that project's venv; the
# dataset path is made absolute first, since it is relative to the repo root.
ROOT=$(git rev-parse --show-toplevel)

# A dirty sweep's rows are excluded from published numbers, so the summary would
# print nothing at all after a smoke run. Ask for them explicitly instead.
SUMMARY_ARGS=()
[ -n "${OPENBETA_TOKENS_ALLOW_DIRTY:-}" ] && SUMMARY_ARGS+=(--include-dirty)

(cd evals && uv run python -m tokens.analysis "$ROOT/$OUT_TOKENS" "${SUMMARY_ARGS[@]}") || true
