from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import pandas as pd

from common.jsonl import read_df

# Below this, a p95 sits within a hair of the maximum and describes one unlucky
# call rather than a tail. Reported rather than silently tolerated.
MIN_MEANINGFUL_N = 20


def summarise(df: pd.DataFrame, key: str) -> pd.DataFrame:
    """Latency and round-trip counts per group."""
    g = df.groupby(key)
    out = pd.DataFrame(
        {
            "n": g.size(),
            "ms_mean": g["ms"].mean(),
            "ms_p50": g["ms"].quantile(0.50),
            "ms_p95": g["ms"].quantile(0.95),
            "ms_max": g["ms"].max(),
            "rt_mean": g["roundtrips"].mean(),
            "rt_max": g["roundtrips"].max(),
            "fails": g["err"].sum(),
        }
    )
    return out.sort_index()


def summarise_run(df: pd.DataFrame) -> pd.DataFrame:
    """Per-run shape: what one invocation of scripts/bench.sh covered.

    A run is one commit's worth of sampling, so the useful columns are coverage
    and wall clock rather than a latency tail.
    """
    ts = pd.to_datetime(df["ts"], format="ISO8601", utc=True)
    g = df.assign(ts=ts).groupby("run")
    return pd.DataFrame(
        {
            "n": g.size(),
            "tools": g["tool"].nunique(),
            "args": g["args_sha"].nunique(),
            "fails": g["err"].sum(),
            "roundtrips": g["roundtrips"].sum(),
            "ms_p50": g["ms"].quantile(0.50),
            "ms_p95": g["ms"].quantile(0.95),
            "start": g["ts"].min(),
            "wall_s": (g["ts"].max() - g["ts"].min()).dt.total_seconds(),
            "commit": g["commit"].first().str[:7],
        }
    ).sort_values("start")


def warn_small(summary: pd.DataFrame) -> None:
    """Flag groups too small for their p95 to mean anything."""
    small = summary[summary["n"] < MIN_MEANINGFUL_N]
    if not small.empty:
        names = ", ".join(f"{name} (n={row.n})" for name, row in small.iterrows())
        print(
            f"\nwarning: {names} below n={MIN_MEANINGFUL_N}; "
            "read p95 as close to max, not as a tail estimate",
            file=sys.stderr,
        )


def main() -> int:
    parser = argparse.ArgumentParser(description="Summarise round-trip measurements from a bench dataset.")
    parser.add_argument("path", type=Path, help="path to data.jsonl")
    parser.add_argument(
        "--by",
        default="tool",
        choices=("tool", "run", "commit", "args_sha"),
        help="grouping key",
    )
    parser.add_argument(
        "--include-dirty",
        action="store_true",
        help="include samples recorded from a modified working tree (excluded by default)",
    )
    parser.add_argument("--json", action="store_true", help="emit the summary as JSON")
    args = parser.parse_args()

    if not args.path.exists():
        print(f"{args.path}: no such file", file=sys.stderr)
        return 1

    df, malformed = read_df(args.path)

    # A sample from a modified tree carries a commit that does not describe the
    # code that produced it, so it cannot back a published number.
    dropped = 0
    if not args.include_dirty and "dirty" in df:
        kept = df[~df["dirty"].fillna(False)]
        dropped = len(df) - len(kept)
        df = kept

    if df.empty:
        print("no samples", file=sys.stderr)
        return 1

    summary = summarise_run(df) if args.by == "run" else summarise(df, args.by)

    if args.json:
        print(json.dumps(
            {
                "rows": json.loads(summary.reset_index().to_json(orient="records", date_format="iso")),
                "dropped_dirty": dropped,
                "malformed": malformed,
            },
            indent=2,
        ))
        return 0

    with pd.option_context("display.width", 200, "display.max_columns", None):
        print(summary.to_string(float_format="{:.2f}".format))
    warn_small(summary)
    print(f"\n{len(df)} samples, {dropped} dirty excluded, {malformed} malformed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
