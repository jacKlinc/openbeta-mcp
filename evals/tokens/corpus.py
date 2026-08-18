from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import logging
import re
from pathlib import Path
from typing import Any

from common.client import mcp_session, text_of

logger = logging.getLogger(__name__)

EVALS_ROOT = Path(__file__).parents[1]
REPO_ROOT = EVALS_ROOT.parent
GAZETTEER = REPO_ROOT / "internal" / "geo" / "gazetteer.go"
AREAS_JSON = EVALS_ROOT / "corpus" / "areas.json"

# 5 km is the benchmark radius, where several origins return nothing at all;
# 50 km is wide enough to saturate the MaxCrags = 20 fan-out cap. The spread
# between them is the cost knob a caller actually turns.
DISTANCES_KM = [5, 20, 50]

# The grade window from the round-trip benchmark, kept identical so find_climbs
# rows here are comparable with the ones in data/round-trip/.
GRADE_WINDOW = {"minGrade": "5.8", "maxGrade": "5.11a"}

# Seeds for the area crawl: Stawamus Chief and the four children pinned in
# internal/mcpserver/bench_test.go, spanning 8 to 201 climbs.
AREA_SEEDS = [
    "8f267065-fc1a-59ce-bcf1-6e9335548363",  # Stawamus Chief, 32 children
    "fbe1956f-65c2-5515-a26f-127bf15fe598",  # Grand Wall Boulders, 201 climbs
    "7f74ea62-664e-581e-a929-f01f6bf68f37",  # Apron Boulders, 55 climbs
    "17a692c8-9e34-5511-90e7-44ef23d10fa1",  # The Apron, 51 climbs
    "e0d61bef-a560-5b18-88ea-7068dabc2bb2",  # Olesen Creek Wall, 8 climbs
]

CRAWL_TARGET = 150

# The gazetteer table is compiled into the binary and has held ~48 entries since
# it was written. A parse that suddenly finds three names has matched the wrong
# block, and a corpus silently shrinking to three places would look like a
# result rather than a bug.
MIN_PLACES = 30


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
    if len(names) < MIN_PLACES:
        raise RuntimeError(
            f"{GAZETTEER}: parsed only {len(names)} places, expected at least {MIN_PLACES}"
        )
    return names


def area_ids() -> list[str]:
    """UUIDs from the committed crawl, falling back to the seeds."""
    if AREAS_JSON.exists():
        return json.loads(AREAS_JSON.read_text())["areas"]
    logger.warning("%s missing; falling back to %d seeds", AREAS_JSON, len(AREA_SEEDS))
    return list(AREA_SEEDS)


def build() -> dict[str, list[dict[str, Any]]]:
    """The full corpus, one list of argument dicts per tool."""
    fan_out = [
        {"place": place, "maxDistanceKm": km} for place in places() for km in DISTANCES_KM
    ]
    return {
        "crags_near": fan_out,
        "find_climbs": [args | GRADE_WINDOW for args in fan_out],
        "get_area_details": [{"areaId": uuid} for uuid in area_ids()],
    }


def canonical(args: dict[str, Any]) -> str:
    """Arguments as Go's encoding/json would render them.

    Go marshals map keys sorted, without spaces, and escapes <, > and & as \\u
    sequences. Reproduced exactly so that args_sha here matches the args_sha the
    server writes for the same call, and the two datasets join on it.
    """
    encoded = json.dumps(args, sort_keys=True, separators=(",", ":"))
    return encoded.replace("<", "\\u003c").replace(">", "\\u003e").replace("&", "\\u0026")


def args_sha(args: dict[str, Any]) -> str:
    """First 12 hex of the SHA-256 of the canonical arguments.

    Matches argsSHA in internal/mcpserver/metrics.go.
    """
    return hashlib.sha256(canonical(args).encode()).hexdigest()[:12]


def _child_uuids(payload: Any) -> list[str]:
    """UUIDs of an area's children, from a get_area_details payload."""
    area = payload.get("area") if isinstance(payload, dict) else None
    if not isinstance(area, dict):
        return []
    children = area.get("children") or []
    return [c["uuid"] for c in children if isinstance(c, dict) and c.get("uuid")]


async def crawl(target: int = CRAWL_TARGET, delay: float = 0.2) -> list[str]:
    """Breadth-first walk from the seeds, collecting area UUIDs.

    Breadth-first on purpose: it sweeps each level of the hierarchy before
    descending, so the corpus mixes big sub-area listings with the leaf crags
    below them instead of running straight down one branch.
    """
    seen: list[str] = []
    known: set[str] = set()
    queue = list(AREA_SEEDS)

    async with mcp_session() as session:
        while queue and len(seen) < target:
            uuid = queue.pop(0)
            if uuid in known:
                continue
            known.add(uuid)
            seen.append(uuid)

            result = await session.call_tool("get_area_details", {"areaId": uuid})
            if result.is_error:
                logger.warning("crawl: %s failed, skipping", uuid)
                continue

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
    parser.add_argument("--limit", type=int, default=CRAWL_TARGET, help="area UUIDs to collect")
    args = parser.parse_args()

    if args.crawl:
        collected = asyncio.run(crawl(args.limit))
        AREAS_JSON.parent.mkdir(parents=True, exist_ok=True)
        AREAS_JSON.write_text(
            json.dumps({"seeds": AREA_SEEDS, "areas": collected}, indent=2) + "\n"
        )
        print(f"{len(collected)} area UUIDs written to {AREAS_JSON}")
        return

    for tool, arg_sets in build().items():
        print(f"{tool:<17} {len(arg_sets):4d} argument sets")


if __name__ == "__main__":
    main()
