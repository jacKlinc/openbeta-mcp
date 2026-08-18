#!/usr/bin/env python3
"""Summarise round-trip measurements from data/round-trip/data.jsonl.

Standard library only, deliberately: this is a group-by and a percentile, and
the evals venv carries the MCP SDK rather than a dataframe stack. If plots are
wanted later, `uv add pandas matplotlib jupyter` and this becomes a notebook's
first cell.

    python3 evals/analysis/roundtrips.py data/round-trip/data.jsonl
    python3 evals/analysis/roundtrips.py --by commit data/round-trip/data.jsonl
"""

from __future__ import annotations

import argparse
import json
import statistics
import sys
from collections import defaultdict
from pathlib import Path

# Below this, a p95 sits within a hair of the maximum and describes one unlucky
# call rather than a tail. Reported rather than silently tolerated.
MIN_MEANINGFUL_N = 20


def load(path: Path) -> tuple[list[dict], int]:
    """Return records and the count of unparseable lines.

    The file is append-only and written by a long-lived server, so a truncated
    final line is plausible. A bad line is reported and skipped, never fatal.
    """
    records: list[dict] = []
    malformed = 0

    with path.open() as handle:
        for lineno, line in enumerate(handle, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError as exc:
                malformed += 1
                print(f"{path}:{lineno}: skipping malformed line: {exc}", file=sys.stderr)

    return records, malformed


def percentile(values: list[float], pct: float) -> float:
    """Interpolated percentile.

    statistics.quantiles interpolates between observations, so at n=20 a p95
    falls between the top two samples. That is a property of the sample size,
    not a defect, but it is why small groups are flagged.
    """
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]
    cuts = statistics.quantiles(values, n=100, method="inclusive")
    return cuts[int(pct) - 1]


def summarise(records: list[dict], key: str) -> list[dict]:
    groups: dict[str, list[dict]] = defaultdict(list)
    for record in records:
        groups[str(record.get(key, ""))].append(record)

    rows = []
    for name, group in sorted(groups.items()):
        ms = sorted(float(r.get("ms", 0)) for r in group)
        roundtrips = [int(r.get("roundtrips", 0)) for r in group]
        rows.append(
            {
                key: name,
                "n": len(group),
                "ms_mean": statistics.fmean(ms),
                "ms_p50": percentile(ms, 50),
                "ms_p95": percentile(ms, 95),
                "ms_max": max(ms),
                "rt_mean": statistics.fmean(roundtrips),
                "rt_max": max(roundtrips),
                "fails": sum(1 for r in group if r.get("err")),
            }
        )
    return rows


def render(rows: list[dict], key: str) -> None:
    width = max([len(key)] + [len(str(r[key])) for r in rows])
    header = (
        f"{key:<{width}}  {'n':>4}  {'mean ms':>9}  {'p50 ms':>9}  "
        f"{'p95 ms':>9}  {'max ms':>9}  {'rt mean':>8}  {'rt max':>7}  {'fails':>6}"
    )
    print(header)
    print("-" * len(header))
    for row in rows:
        print(
            f"{row[key]:<{width}}  {row['n']:>4}  {row['ms_mean']:>9.1f}  "
            f"{row['ms_p50']:>9.1f}  {row['ms_p95']:>9.1f}  {row['ms_max']:>9.1f}  "
            f"{row['rt_mean']:>8.2f}  {row['rt_max']:>7}  {row['fails']:>6}"
        )

    small = [r for r in rows if r["n"] < MIN_MEANINGFUL_N]
    if small:
        names = ", ".join(f"{r[key]} (n={r['n']})" for r in small)
        print(
            f"\nwarning: {names} below n={MIN_MEANINGFUL_N}; "
            "read p95 as close to max, not as a tail estimate",
            file=sys.stderr,
        )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("path", type=Path, help="path to data.jsonl")
    parser.add_argument("--by", default="tool", choices=("tool", "commit", "args_sha"), help="grouping key")
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

    records, malformed = load(args.path)

    # A sample from a modified tree carries a commit that does not describe the
    # code that produced it, so it cannot back a published number.
    dropped = 0
    if not args.include_dirty:
        kept = [r for r in records if not r.get("dirty")]
        dropped = len(records) - len(kept)
        records = kept

    if not records:
        print("no samples", file=sys.stderr)
        return 1

    rows = summarise(records, args.by)

    if args.json:
        print(json.dumps({"rows": rows, "dropped_dirty": dropped, "malformed": malformed}, indent=2))
        return 0

    render(rows, args.by)
    print(f"\n{len(records)} samples, {dropped} dirty excluded, {malformed} malformed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
