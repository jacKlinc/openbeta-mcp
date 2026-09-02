"""Derive the golden set's expected values from the local snapshot. See judge/README.md."""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
from pathlib import Path

from common.client import mcp_session, text_of
from common.config import get_settings
from judge.dataset import GoldenSet
from judge.models import Case, Generated, ScalarExpected, SetExpected
from judge.payload import GroundTruthError, extract, fingerprint
from tokens.corpus import args_sha

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

JUDGE_ROOT = Path(__file__).parent
DATA = JUDGE_ROOT / "data"
GOLDEN_SET = DATA / "golden-set.jsonl"
MANIFEST = DATA / "manifest.json"


async def generate(cases: list[Case]) -> dict[str, str]:
    """Fill in each deterministic case's Generated block. Returns the fingerprints.

    The endpoint is passed to the subprocess explicitly rather than left to its own
    default, so the endpoint the manifest records is the one the calls went to.
    """
    prints: dict[str, str] = {}
    generable = [c for c in cases if isinstance(c.expected, SetExpected | ScalarExpected)]
    logger.info("generating %d of %d cases; the rest are prose", len(generable), len(cases))

    async with mcp_session(env=get_settings().server_env()) as session:
        for case in generable:
            query = case.expected.query

            result = await session.call_tool(query.tool, query.args)
            text = text_of(result)
            if result.is_error:
                raise GroundTruthError(
                    f"{case.case_id}: {query.tool} errored, which no set/scalar case expects: {text}"
                )

            payload = json.loads(text)
            value = extract(query.tool, case.expected.kind, payload)

            # A happy_path case the model could pass by finding nothing.
            if case.category == "happy_path" and case.expected.kind == "set" and not value:
                raise GroundTruthError(
                    f"{case.case_id}: {query.tool} returned nothing for a happy_path case. "
                    "Point it at a place the snapshot covers, or move it out of happy_path."
                )

            case.expected.generated = Generated(value=value, args_sha=args_sha(query.args))

            if fp := fingerprint(query.tool, payload):
                prints[case.case_id] = fp

            logger.info(
                "%s: %s -> %s",
                case.case_id,
                query.tool,
                f"{len(value)} items" if isinstance(value, list) else value,
            )

    return prints


def check(golden: GoldenSet, prints: dict[str, str]) -> int:
    """Compare fresh fingerprints against the manifest.

    Returns:
        0 when every recorded fingerprint matches, 1 on drift, which is the
        process exit code.
    """
    recorded = golden.manifest().fingerprint
    if not recorded:
        logger.error("manifest holds no fingerprints; run without --check first")
        return 1

    drifted = sorted(cid for cid, fp in prints.items() if recorded.get(cid) != fp)
    missing = sorted(set(recorded) - set(prints))
    for cid in drifted:
        logger.error("%s: data drifted (%s -> %s)", cid, recorded.get(cid), prints[cid])
    for cid in missing:
        logger.error("%s: recorded but returned nothing this run", cid)

    if drifted or missing:
        return 1
    logger.info("all %d fingerprints match", len(prints))
    return 0


def main() -> int:
    """Generate, or with --check verify that nothing drifted."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="verify against the manifest, write nothing")
    args = parser.parse_args()

    logger.info("endpoint %s", get_settings().openbeta_endpoint)
    golden = GoldenSet()
    cases = golden.cases()
    prints = asyncio.run(generate(cases))

    if args.check:
        return check(golden, prints)

    golden.write(cases, prints)
    logger.info("wrote %d cases and %d fingerprints", len(cases), len(prints))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
