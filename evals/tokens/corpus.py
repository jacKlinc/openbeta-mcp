from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import logging
import os
import re
from collections import Counter
from pathlib import Path
from typing import Any

from common.client import mcp_session, text_of

logger = logging.getLogger(__name__)

EVALS_ROOT = Path(__file__).parents[1]
REPO_ROOT = EVALS_ROOT.parent
GAZETTEER = REPO_ROOT / "internal" / "geo" / "gazetteer.go"
AREAS_JSON = EVALS_ROOT / "corpus" / "areas.json"
ORIGINS_JSON = EVALS_ROOT / "corpus" / "origins.json"


class Sampling:
    """The knobs that decide which calls the corpus makes.

    Shaped around one property of the data: crags come in clumps. Across the US
    destinations at nine radii, only 17 of 162 place-radius pairs hold between 1
    and 19 crags, and every one of those sits at 8 km or under. So a place is
    walked up the ladder until its crag count stops moving, and only the rungs
    whose payloads differ are kept. Reasoning in docs/findings/corpus/README.md.

    DISTANCES_KM   The ladder. Starts at 1 km: a ladder starting at 5 km sits in
                   the saturated regime almost everywhere, which is what the old
                   place-crossed-with-radii corpus was measuring.
    PLATEAU_RUNGS  Equal counts in a row that end the climb. Past the plateau
                   every radius returns the same nearest crags.
    EMPTIES        Empty results kept. One is a real cost worth recording; the
                   sixty the old corpus held were the same number sixty times.
    CRAWL_TARGET   Area UUIDs to collect for get_area_details.
    MIN_PLACES     The gazetteer has held ~48 entries since it was written. A
                   parse finding three has matched the wrong block, and a corpus
                   silently shrinking would look like a result rather than a bug.
    """

    DISTANCES_KM = [1, 2, 3, 5, 8, 12, 20, 35, 50]
    PLATEAU_RUNGS = 2
    EMPTIES = 5
    CRAWL_TARGET = 150
    MIN_PLACES = 30


# US destinations only: non-US areas carry the missing leaf data of
# openbeta-graphql#489, which shrinks payloads and confounds a cost measurement.
# Names must match the gazetteer table; candidates() checks that they do.
US_PLACES = [
    "Bishop",
    "Yosemite",
    "Joshua Tree",
    "Red Rocks",
    "Indian Creek",
    "Moab",
    "Index",
    "Leavenworth",
    "Smith Rock",
    "Red River Gorge",
    "New River Gorge",
    "The Gunks",
    "Rumney",
    "Boulder",
    "Estes Park",
    "Ten Sleep",
    "Lander",
    "Hueco Tanks",
]

# The grade window from the round-trip benchmark, kept identical so find_climbs
# rows here are comparable with the ones in data/round-trip/.
GRADE_WINDOW = {"minGrade": "5.8", "maxGrade": "5.11a"}

# Seeds for the area crawl: Yosemite Valley and its four largest children,
# spanning 322 to 1505 climbs. US like the rest of the corpus -- the Squamish
# seeds this carried before are absent from a local stack, where every call
# against one failed and the tool measured nothing.
AREA_SEEDS = [
    "0f1eddf1-5a79-556e-92f6-0d91627e1f2f",  # Yosemite Valley, 1505 climbs
    "14a34046-d4ba-5064-b505-92d1514f96b6",  # Lower Merced River Canyon, 428
    "5e3c95f8-ac0c-58d7-8278-934731f6453b",  # Valley North Side, 405
    "587984eb-9d0d-54e5-9de5-b9c559f735d2",  # Valley South Side, 350
    "662fc058-2269-5b7a-b715-71dcd4f34c93",  # Yosemite Valley Bouldering, 322
]


def places() -> list[str]:
    """Destination names from the gazetteer table compiled into the server.

    Parsed from the Go source rather than duplicated here: a name this harness
    invents is a name the server cannot resolve, and the table is the only place
    the two agree on spelling.
    """
    source = GAZETTEER.read_text()
    block = re.search(r"var destinations = map\[string\]Point\{(.*?)\n\}", source, re.S)
    if not block:
        raise RuntimeError(f"{GAZETTEER}: destinations table not found")

    names = re.findall(r'^\s*"([^"]+)":', block.group(1), re.M)
    if len(names) < Sampling.MIN_PLACES:
        raise RuntimeError(f"{GAZETTEER}: parsed only {len(names)} places, expected at least {Sampling.MIN_PLACES}")
    return names


def build() -> dict[str, list[dict[str, Any]]]:
    """The full corpus, one list of argument dicts per tool.

    Each place brings its own radii rather than a shared list: which radii change
    the payload depends on how the crags around that place are spread.
    """
    # Both committed artefacts, both with a fallback: an unprobed checkout still
    # produces a corpus, just a blunter one, rather than failing at import.
    if ORIGINS_JSON.exists():
        origins = json.loads(ORIGINS_JSON.read_text())["origins"]
    else:
        logger.warning("%s missing; falling back to %d unprobed places", ORIGINS_JSON, len(US_PLACES))
        origins = [{"place": p, "radii": [Sampling.DISTANCES_KM[-1]]} for p in US_PLACES]

    if AREAS_JSON.exists():
        areas = json.loads(AREAS_JSON.read_text())["areas"]
    else:
        logger.warning("%s missing; falling back to %d seeds", AREAS_JSON, len(AREA_SEEDS))
        areas = list(AREA_SEEDS)

    fan_out = [{"place": o["place"], "maxDistanceKm": km} for o in origins for km in o["radii"]]
    return {
        "crags_near": fan_out,
        "find_climbs": [args | GRADE_WINDOW for args in fan_out],
        "get_area_details": [{"areaId": uuid} for uuid in areas],
    }


def args_sha(args: dict[str, Any]) -> str:
    """First 12 hex of the SHA-256 of the arguments, as argsSHA computes it.

    Matches argsSHA in internal/mcpserver/metrics.go, so the harness dataset and
    the server's own sink join on (tool, args_sha). Go marshals map keys sorted,
    without spaces, and escapes <, > and & as \\u sequences -- reproduced here
    exactly, because a different rendering is a different hash and the two
    datasets would stop joining.
    """
    encoded = json.dumps(args, sort_keys=True, separators=(",", ":"))
    canonical = encoded.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026")
    return hashlib.sha256(canonical.encode()).hexdigest()[:12]


def _child_uuids(payload: Any) -> list[str]:
    """UUIDs of an area's children, from a get_area_details payload."""
    area = payload.get("area") if isinstance(payload, dict) else None
    if not isinstance(area, dict):
        return []
    children = area.get("children") or []
    return [c["uuid"] for c in children if isinstance(c, dict) and c.get("uuid")]


def candidates() -> list[str]:
    """The places worth probing, checked against the table that resolves them."""
    known = set(places())
    missing = [p for p in US_PLACES if p not in known]
    if missing:
        raise RuntimeError(f"{GAZETTEER}: US_PLACES not in the table: {missing}")
    return list(US_PLACES)


async def probe(limit: int | None = None, delay: float = 0.0) -> list[dict[str, Any]]:
    """Walk each place up the ladder, recording the crag count at every rung.

    Probed at the shipped cap: the count a caller is charged for is the one that
    matters, not the true number of crags out there.
    """
    probed: list[dict[str, Any]] = []
    failed = 0

    async with mcp_session() as session:
        for place in candidates()[:limit]:
            ladder: list[dict[str, Any]] = []
            for km in Sampling.DISTANCES_KM:
                call = {"place": place, "maxDistanceKm": km}
                try:
                    result = await session.call_tool("crags_near", call)
                    payload = json.loads(text_of(result))
                    count = int(payload.get("count", 0)) if not result.is_error else 0
                except (json.JSONDecodeError, ValueError, KeyError) as exc:
                    logger.warning("probe: %s failed (%s), skipping", call, exc)
                    failed += 1
                    continue

                # Empty, still finding crags, or settled. A count that stops
                # rising is the cap binding, not the crags running out.
                if count == 0:
                    band = "empty"
                elif not ladder or count > ladder[-1]["count"]:
                    band = "growing"
                else:
                    band = "plateau"

                ladder.append({"km": km, "count": count, "band": band})
                await asyncio.sleep(delay)

                flat = [r["band"] for r in ladder[-Sampling.PLATEAU_RUNGS :]]
                if len(flat) == Sampling.PLATEAU_RUNGS and set(flat) == {"plateau"}:
                    break

            probed.append({"place": place, "ladder": ladder})
            logger.info("probe: %-17s %s", place, " ".join(f"{r['km']}km={r['count']}" for r in ladder))

    # A wrong argument name fails every call the same way, and a corpus quietly
    # built from the handful that worked would look like a result. Anything past
    # a fifth is systematic, not a dead origin.
    if failed > len(probed) // 4:
        raise RuntimeError(f"probe: {failed} calls failed across {len(probed)} places; check the arguments")

    return probed


def radii_for(ladder: list[dict[str, Any]]) -> list[int]:
    """Two radii whose payloads differ, or one when none do.

    The lower rung has to carry strictly fewer crags than the settled one. "The
    last radius that was still growing" looks like the right pick and is not: it
    already holds the plateau's count, which is what makes the next rung a
    plateau, so the pair comes back byte-identical.

    Empty rungs are not eligible: select() pins a few of those deliberately.
    """
    if not ladder:
        return []

    settled = ladder[-1]["count"]
    chosen = [next(r["km"] for r in ladder if r["count"] == settled)]

    lower = [r["km"] for r in ladder if 0 < r["count"] < settled]
    if lower:
        chosen.append(lower[-1])
    return sorted(set(chosen))


def select(probed: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """One entry per place, carrying the radii to call it at.

    No place is empty at every radius, so the pinned empties are the widest rung
    that still found nothing -- the one just below where the crags start.
    """
    entries = [{"place": p["place"], "radii": radii_for(p["ladder"]), "ladder": p["ladder"]} for p in probed]

    pinned = 0
    for entry, place in zip(entries, probed, strict=True):
        if pinned >= Sampling.EMPTIES:
            break
        empties = [r["km"] for r in place["ladder"] if r["band"] == "empty"]
        if empties:
            entry["radii"] = sorted({*entry["radii"], empties[-1]})
            pinned += 1

    return entries


async def crawl(target: int = Sampling.CRAWL_TARGET, delay: float = 0.2) -> list[str]:
    """Breadth-first walk from the seeds, collecting area UUIDs.

    Breadth-first on purpose: it sweeps each level of the hierarchy before
    descending, so the corpus mixes big sub-area listings with the leaf crags
    below them instead of running straight down one branch.
    """
    # `sweep --limit N` measures the top N areas of the hierarchy
    seen: list[str] = []
    known: set[str] = set()
    queue = list(AREA_SEEDS)

    async with mcp_session() as session:
        while queue and len(seen) < target:
            # Marked known before call, not after: a UUID that fails is not worth a second attempt through another parent
            uuid = queue.pop(0)
            if uuid in known:
                continue
            known.add(uuid)

            # An area that did not answer is not an area to measure
            result = await session.call_tool("get_area_details", {"areaId": uuid})
            if result.is_error:
                logger.warning("crawl: %s failed, skipping", uuid)
                continue
            seen.append(uuid)

            # Kept even if the children cannot be read: the area itself answered, so it is a valid call to measure
            try:
                payload = json.loads(text_of(result))
            except json.JSONDecodeError:
                logger.warning("crawl: %s returned non-JSON, skipping", uuid)
                continue

            queue.extend(u for u in _child_uuids(payload) if u not in known)
            logger.info("crawl: %3d/%d collected, %d queued", len(seen), target, len(queue))
            await asyncio.sleep(delay)

    return seen


def main() -> None:
    logging.basicConfig(level=logging.INFO)
    parser = argparse.ArgumentParser(description="Build or inspect the token sweep corpus.")
    parser.add_argument("--crawl", action="store_true", help="rebuild corpus/areas.json (live calls)")
    parser.add_argument("--probe", action="store_true", help="rebuild corpus/origins.json (live calls)")
    parser.add_argument("--limit", type=int, default=Sampling.CRAWL_TARGET, help="area UUIDs to collect")
    args = parser.parse_args()

    if args.probe:
        probed = select(asyncio.run(probe(args.limit if args.limit != Sampling.CRAWL_TARGET else None)))
        ORIGINS_JSON.parent.mkdir(parents=True, exist_ok=True)
        ORIGINS_JSON.write_text(
            json.dumps(
                {
                    "probe": {
                        "ladder_km": Sampling.DISTANCES_KM,
                        "plateau_rungs": Sampling.PLATEAU_RUNGS,
                        "endpoint": os.environ.get("OPENBETA_ENDPOINT", "public"),
                        "max_crags": os.environ.get("OPENBETA_MAX_CRAGS", "default"),
                    },
                    "origins": probed,
                },
                indent=2,
            )
            + "\n"
        )
        counts = Counter(r["band"] for o in probed for r in o.get("ladder", []))
        calls = sum(len(o["radii"]) for o in probed)
        print(f"{len(probed)} origins ({calls} calls) written to {ORIGINS_JSON}: {dict(counts)}")
        return

    if args.crawl:
        collected = asyncio.run(crawl(args.limit))
        AREAS_JSON.parent.mkdir(parents=True, exist_ok=True)
        AREAS_JSON.write_text(json.dumps({"seeds": AREA_SEEDS, "areas": collected}, indent=2) + "\n")
        print(f"{len(collected)} area UUIDs written to {AREAS_JSON}")
        return

    for tool, arg_sets in build().items():
        print(f"{tool:<17} {len(arg_sets):4d} argument sets")


if __name__ == "__main__":
    main()
