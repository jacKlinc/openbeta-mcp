from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import matplotlib
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd

from common.jsonl import read_df

# Below this a p99 describes one unlucky call rather than a tail. The token
# corpus is wide enough to clear it comfortably; a group that does not has
# usually been cut down by --limit.
MIN_MEANINGFUL_N = 20

# Categorical slots 1-3 of the reference palette, in fixed order: identity
# follows the tool, so a filtered-out tool never repaints the others.
TOOL_COLOURS = {
    "crags_near": "#2a78d6",
    "find_climbs": "#eb6834",
    "get_area_details": "#1baf7a",
}
SURFACE = "#fcfcfb"
INK = "#1a1a19"
MUTED = "#6b6b63"


def summarise(df: pd.DataFrame, key: str) -> pd.DataFrame:
    """Token cost per group, from the median out to the tail."""
    g = df.groupby(key)
    return pd.DataFrame(
        {
            "n": g.size(),
            "tok_mean": g["tokens"].mean(),
            "tok_p50": g["tokens"].quantile(0.50),
            "tok_p90": g["tokens"].quantile(0.90),
            "tok_p95": g["tokens"].quantile(0.95),
            "tok_p99": g["tokens"].quantile(0.99),
            "tok_max": g["tokens"].max(),
            "chars_mean": g["chars"].mean(),
            "fails": g["err"].sum(),
        }
    ).sort_index()


def breaking_points(df: pd.DataFrame) -> list[str]:
    """One line per tool: the tail, and the call that defines it."""
    lines = []
    for tool, group in df.groupby("tool"):
        p99 = group["tokens"].quantile(0.99)
        worst = group.loc[group["tokens"].idxmax()]
        lines.append(
            f"{tool:<17} p99 {p99:>7,.0f} tokens   worst {worst['tokens']:>7,.0f} "
            f"at {json.dumps(worst['args'], separators=(',', ':'))}"
        )
    return lines


def _style(ax: plt.Axes, title: str, xlabel: str, ylabel: str) -> None:
    ax.set_title(title, color=INK, fontsize=11, loc="left", pad=12)
    ax.set_xlabel(xlabel, color=MUTED, fontsize=9)
    ax.set_ylabel(ylabel, color=MUTED, fontsize=9)
    ax.tick_params(colors=MUTED, labelsize=8, length=0)
    ax.grid(axis="y", color="#e5e5e0", linewidth=0.8)
    ax.set_axisbelow(True)
    for side, spine in ax.spines.items():
        spine.set_visible(side == "bottom")
        spine.set_color("#d8d8d2")


def _figure(size: tuple[float, float]) -> tuple[plt.Figure, plt.Axes]:
    fig, ax = plt.subplots(figsize=size, dpi=160, facecolor=SURFACE)
    ax.set_facecolor(SURFACE)
    return fig, ax


def plot_histograms(df: pd.DataFrame, out: Path) -> list[Path]:
    """One histogram per tool, with the tail marked.

    Log x because the payloads span two orders of magnitude — on a linear axis
    the small calls collapse into the first bin and the shape disappears.
    """
    written = []
    for tool, group in df.groupby("tool"):
        tokens = group.loc[group["tokens"] > 0, "tokens"]
        if tokens.empty:
            continue

        fig, ax = _figure((7, 3.6))
        bins = np.logspace(np.log10(tokens.min()), np.log10(tokens.max()), 30)
        ax.hist(tokens, bins=bins, color=TOOL_COLOURS.get(tool, "#2a78d6"), edgecolor=SURFACE, linewidth=0.8)
        ax.set_xscale("log")

        for pct, style in ((0.50, ":"), (0.95, "--"), (0.99, "-")):
            value = tokens.quantile(pct)
            ax.axvline(value, color=INK, linewidth=1.2, linestyle=style, alpha=0.7)
            ax.annotate(
                f"p{int(pct * 100)} {value:,.0f}",
                xy=(value, ax.get_ylim()[1]),
                xytext=(3, -10),
                textcoords="offset points",
                color=INK,
                fontsize=8,
            )

        _style(ax, f"{tool} — token distribution (n={len(group)})", "tokens per call (log)", "calls")
        path = out / f"tokens-hist-{tool.replace('_', '-')}.png"
        fig.tight_layout()
        fig.savefig(path, facecolor=SURFACE)
        plt.close(fig)
        written.append(path)
    return written


def plot_ecdf(df: pd.DataFrame, out: Path) -> Path:
    """All tools on one axis: the knee is the breaking point."""
    fig, ax = _figure((7.5, 4.2))

    for tool, group in df.groupby("tool"):
        tokens = np.sort(group["tokens"].to_numpy())
        share = np.arange(1, len(tokens) + 1) / len(tokens)
        colour = TOOL_COLOURS.get(tool, "#2a78d6")
        # Stepped, and started at zero share: an ECDF is a step function, and a
        # smooth line implies costs between the observed ones that never occur.
        ax.step(
            np.concatenate(([tokens[0]], tokens)),
            np.concatenate(([0.0], share)),
            where="post",
            color=colour,
            linewidth=2,
            label=tool,
            solid_capstyle="round",
        )

    ax.set_xscale("log")
    ax.set_ylim(0, 1.05)
    # The three curves converge at the top, so end-of-line labels would collide;
    # the legend carries identity here instead.
    ax.axhline(0.99, color=MUTED, linewidth=0.8, linestyle="--")
    ax.annotate(
        "p99",
        xy=(1.0, 0.99),
        xycoords=("axes fraction", "data"),
        xytext=(-18, 4),
        textcoords="offset points",
        color=MUTED,
        fontsize=8,
    )
    _style(ax, "Share of calls at or below a token cost", "tokens per call (log)", "share of calls")
    ax.legend(frameon=False, fontsize=8, labelcolor=INK, loc="lower right", bbox_to_anchor=(1.0, 0.05))

    path = out / "tokens-ecdf.png"
    fig.tight_layout()
    fig.savefig(path, facecolor=SURFACE)
    plt.close(fig)
    return path


def plot_distance(df: pd.DataFrame, out: Path) -> Path | None:
    """Fan-out cost against the one input knob that should move it.

    Every point is drawn, jittered, rather than a mean per radius: the spread
    across origins at one radius is larger than the shift between radii, and a
    line of means would hide exactly that.
    """
    fan_out = df[df["tool"].isin(["crags_near", "find_climbs"])].copy()
    if fan_out.empty:
        return None
    fan_out["km"] = fan_out["args"].apply(lambda a: a.get("maxDistanceKm"))
    fan_out = fan_out.dropna(subset=["km"])
    if fan_out.empty:
        return None

    radii = sorted(fan_out["km"].unique())
    tools = sorted(fan_out["tool"].unique())
    rng = np.random.default_rng(0)  # fixed: the same figure twice from the same data

    fig, ax = _figure((7.5, 4.2))
    for offset, tool in zip((-0.14, 0.14), tools, strict=False):
        group = fan_out[fan_out["tool"] == tool]
        colour = TOOL_COLOURS.get(tool, "#2a78d6")
        for slot, km in enumerate(radii):
            tokens = group.loc[group["km"] == km, "tokens"]
            if tokens.empty:
                continue
            x = slot + offset + rng.uniform(-0.05, 0.05, len(tokens))
            ax.scatter(x, tokens, s=18, color=colour, alpha=0.55, edgecolor=SURFACE, linewidth=0.5)
            ax.plot(
                [slot + offset - 0.09, slot + offset + 0.09],
                [tokens.median()] * 2,
                color=INK,
                linewidth=2,
                solid_capstyle="round",
            )
        ax.scatter([], [], s=18, color=colour, label=tool)

    ax.set_xticks(range(len(radii)), [f"{km:g} km" for km in radii])
    _style(ax, "Fan-out token cost by search radius (bar = median)", "maxDistanceKm", "tokens per call")
    ax.legend(frameon=False, fontsize=8, labelcolor=INK, loc="upper left")

    path = out / "tokens-vs-distance.png"
    fig.tight_layout()
    fig.savefig(path, facecolor=SURFACE)
    plt.close(fig)
    return path


def main() -> int:
    """Print the per-group table, save the figures, and state the breaking point."""
    # Agg here rather than at import: figures are files, never a window, and a
    # display-less machine must not pick a GUI backend and fail.
    matplotlib.use("Agg")

    parser = argparse.ArgumentParser(description="Summarise the token distribution in a sweep dataset.")
    parser.add_argument("path", type=Path, help="path to data.jsonl")
    parser.add_argument("--by", default="tool", choices=("tool", "run", "commit", "args_sha"))
    parser.add_argument("--plots", type=Path, help="directory for PNGs (default: <path dir>/plots)")
    parser.add_argument("--no-plots", action="store_true")
    parser.add_argument(
        "--include-dirty",
        action="store_true",
        help="include rows recorded from a modified working tree (excluded by default)",
    )
    parser.add_argument("--json", action="store_true", help="emit the summary as JSON")
    args = parser.parse_args()

    if not args.path.exists():
        print(f"{args.path}: no such file", file=sys.stderr)
        return 1

    df, malformed = read_df(args.path)

    dropped = 0
    if not args.include_dirty and "dirty" in df:
        kept = df[~df["dirty"].fillna(False)]
        dropped = len(df) - len(kept)
        df = kept

    if df.empty:
        print("no rows", file=sys.stderr)
        return 1

    summary = summarise(df, args.by)

    if args.json:
        print(json.dumps(
            {
                "rows": json.loads(summary.reset_index().to_json(orient="records")),
                "breaking_points": breaking_points(df),
                "dropped_dirty": dropped,
                "malformed": malformed,
            },
            indent=2,
        ))
        return 0

    with pd.option_context("display.width", 200, "display.max_columns", None):
        print(summary.to_string(float_format="{:,.0f}".format))

    small = summary[summary["n"] < MIN_MEANINGFUL_N]
    if not small.empty:
        names = ", ".join(f"{name} (n={row.n})" for name, row in small.iterrows())
        print(
            f"\nwarning: {names} below n={MIN_MEANINGFUL_N}; "
            "read p99 as close to max, not as a tail estimate",
            file=sys.stderr,
        )

    if not args.no_plots:
        out = args.plots or args.path.parent / "plots"
        out.mkdir(parents=True, exist_ok=True)
        written = plot_histograms(df, out)
        written.append(plot_ecdf(df, out))
        if (distance := plot_distance(df, out)) is not None:
            written.append(distance)
        print("\n" + "\n".join(f"plot: {p}" for p in written))

    print("\nBreaking point")
    print("\n".join(f"  {line}" for line in breaking_points(df)))
    print(f"\n{len(df)} rows, {dropped} dirty excluded, {malformed} malformed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
