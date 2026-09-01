"""Figures for a notebook, over any dataset a sweep writes.

MLflow is the cross-run view and draws the quantile curve well. These are the
shapes it has no form for: a distribution's actual shape, and cost against an
argument the export never logs as a metric. See ../docs/charts.md.

Every function returns a Figure and writes nothing. A notebook renders the
return value; a script calls savefig on it. Nothing here selects a backend --
forcing Agg at import, as the old CLI did, would stop inline rendering.
"""

from __future__ import annotations

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from matplotlib.figure import Figure

# Below this a p99 describes one unlucky call rather than a tail. The token
# corpus is wide enough to clear it comfortably; a group that does not has
# usually been cut down by --limit.
MIN_MEANINGFUL_N = 20

# Categorical slots 1-3 of the reference palette, in fixed order: identity
# follows the tool, so a filtered-out tool never repaints the others. This is
# what MLflow cannot do -- it colours by run, so one run's three tools arrive in
# one colour separated only by dash pattern.
TOOL_COLOURS = {
    "crags_near": "#2a78d6",
    "find_climbs": "#eb6834",
    "get_area_details": "#1baf7a",
}
SURFACE = "#fcfcfb"
INK = "#1a1a19"
MUTED = "#6b6b63"

# What a value column is called on an axis, and whether it wants a log scale.
# The same column-driven dispatch tokens/export.py uses for metric prefixes, so
# one implementation serves the token sweep and the Go bench alike.
MEASURES = {
    "tokens": ("tokens per call", True),
    "ms": ("latency (ms)", True),
    "roundtrips": ("HTTP round trips", False),
}


def _measure(df: pd.DataFrame, column: str, log: bool | None = None) -> tuple[str, bool]:
    """Axis label and scale, having checked the frame actually holds the column.

    The default is `tokens`, so asking a round-trip frame for a figure without
    naming a column is an easy mistake; without this it surfaces as a KeyError
    from inside pandas indexing, several frames deep.
    """
    if column not in df:
        available = ", ".join(sorted(set(MEASURES) & set(df.columns))) or "none"
        raise KeyError(f"no {column!r} column in this dataset; measures present: {available}")
    label, default = MEASURES.get(column, (column, True))
    return label, default if log is None else log


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


def _figure(size: tuple[float, float]) -> tuple[Figure, plt.Axes]:
    fig, ax = plt.subplots(figsize=size, dpi=160, facecolor=SURFACE)
    ax.set_facecolor(SURFACE)
    return fig, ax


def summarise(df: pd.DataFrame, key: str = "tool", column: str = "tokens") -> pd.DataFrame:
    """Cost per group, from the median out to the tail."""
    g = df.groupby(key)
    return pd.DataFrame(
        {
            "n": g.size(),
            "mean": g[column].mean(),
            "p50": g[column].quantile(0.50),
            "p90": g[column].quantile(0.90),
            "p95": g[column].quantile(0.95),
            "p99": g[column].quantile(0.99),
            "max": g[column].max(),
            "fails": g["err"].sum(),
        }
    ).sort_index()


def small_groups(summary: pd.DataFrame) -> pd.DataFrame:
    """The rows whose percentiles do not mean anything yet.

    MLflow will happily chart a p99 over four calls, so this is the guard that
    did not survive the port. Read the p99 of anything listed here as close to
    max, not as a tail estimate.
    """
    return summary[summary["n"] < MIN_MEANINGFUL_N]


def breaking_points(df: pd.DataFrame, column: str = "tokens") -> pd.DataFrame:
    """One row per tool: the tail, and the call that defines it."""
    rows = []
    for tool, group in df.groupby("tool"):
        worst = group.loc[group[column].idxmax()]
        rows.append({"tool": tool, "p99": group[column].quantile(0.99), column: worst[column], "args": worst["args"]})
    return pd.DataFrame(rows).set_index("tool")


def ecdf(df: pd.DataFrame, column: str = "tokens", *, log: bool | None = None) -> Figure:
    """All tools on one axis: the knee is the breaking point."""
    label, log_scale = _measure(df, column, log)
    fig, ax = _figure((7.5, 4.2))

    for tool, group in df.groupby("tool"):
        values = np.sort(group[column].to_numpy())
        share = np.arange(1, len(values) + 1) / len(values)
        # Stepped, and started at zero share: an ECDF is a step function, and a
        # smooth line implies costs between the observed ones that never occur.
        ax.step(
            np.concatenate(([values[0]], values)),
            np.concatenate(([0.0], share)),
            where="post",
            color=TOOL_COLOURS.get(tool, "#2a78d6"),
            linewidth=2,
            label=tool,
            solid_capstyle="round",
        )

    if log_scale:
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
    _style(ax, "Share of calls at or below a cost", f"{label}{' (log)' if log_scale else ''}", "share of calls")
    ax.legend(frameon=False, fontsize=8, labelcolor=INK, loc="lower right", bbox_to_anchor=(1.0, 0.05))
    fig.tight_layout()
    return fig


def histograms(df: pd.DataFrame, column: str = "tokens", *, log: bool | None = None) -> dict[str, Figure]:
    """One histogram per tool, with the tail marked, keyed by tool name.

    Log bins because the payloads span two orders of magnitude -- on a linear
    axis the small calls collapse into the first bin and the shape disappears.
    Round trips are small integers, so MEASURES turns that off for them.
    """
    label, log_scale = _measure(df, column, log)
    figures = {}

    for tool, group in df.groupby("tool"):
        values = group.loc[group[column] > 0, column]
        if values.empty:
            continue

        fig, ax = _figure((7, 3.6))
        if log_scale:
            bins = np.logspace(np.log10(values.min()), np.log10(values.max()), 30)
        else:
            bins = np.arange(values.min(), values.max() + 2) - 0.5
        ax.hist(values, bins=bins, color=TOOL_COLOURS.get(tool, "#2a78d6"), edgecolor=SURFACE, linewidth=0.8)
        if log_scale:
            ax.set_xscale("log")

        for pct, style in ((0.50, ":"), (0.95, "--"), (0.99, "-")):
            value = values.quantile(pct)
            ax.axvline(value, color=INK, linewidth=1.2, linestyle=style, alpha=0.7)
            ax.annotate(
                f"p{int(pct * 100)} {value:,.0f}",
                xy=(value, ax.get_ylim()[1]),
                xytext=(3, -10),
                textcoords="offset points",
                color=INK,
                fontsize=8,
            )

        _style(
            ax,
            f"{tool} — distribution (n={len(group)})",
            f"{label}{' (log)' if log_scale else ''}",
            "calls",
        )
        fig.tight_layout()
        figures[str(tool)] = fig

    return figures


def by_distance(df: pd.DataFrame, column: str = "tokens") -> Figure | None:
    """Fan-out cost against the one input knob that should move it.

    The radius lives in each row's args object and is never logged as a metric,
    so MLflow's metric-against-metric scatter has nothing to put on x. This plot
    only exists here.

    Every point is drawn, jittered, rather than a mean per radius: the spread
    across origins at one radius is larger than the shift between radii, and a
    line of means would hide exactly that. None when no row carries a radius.
    """
    fan_out = df[df["tool"].isin(["crags_near", "find_climbs"])].copy()
    if fan_out.empty:
        return None
    fan_out["km"] = fan_out["args"].apply(lambda a: a.get("maxDistanceKm"))
    fan_out = fan_out.dropna(subset=["km"])
    if fan_out.empty:
        return None

    label, _ = _measure(df, column)
    radii = sorted(fan_out["km"].unique())
    tools = sorted(fan_out["tool"].unique())
    rng = np.random.default_rng(0)  # fixed: the same figure twice from the same data

    fig, ax = _figure((7.5, 4.2))
    for offset, tool in zip((-0.14, 0.14), tools, strict=False):
        group = fan_out[fan_out["tool"] == tool]
        colour = TOOL_COLOURS.get(tool, "#2a78d6")
        for slot, km in enumerate(radii):
            values = group.loc[group["km"] == km, column]
            if values.empty:
                continue
            x = slot + offset + rng.uniform(-0.05, 0.05, len(values))
            ax.scatter(x, values, s=18, color=colour, alpha=0.55, edgecolor=SURFACE, linewidth=0.5)
            ax.plot(
                [slot + offset - 0.09, slot + offset + 0.09],
                [values.median()] * 2,
                color=INK,
                linewidth=2,
                solid_capstyle="round",
            )
        ax.scatter([], [], s=18, color=colour, label=tool)

    ax.set_xticks(range(len(radii)), [f"{km:g} km" for km in radii])
    _style(ax, "Fan-out cost by search radius (bar = median)", "maxDistanceKm", label)
    ax.legend(frameon=False, fontsize=8, labelcolor=INK, loc="upper left")
    fig.tight_layout()
    return fig
