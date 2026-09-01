"""Generate expected values for the golden set from the pinned local stack.

Hand-written ground truth drifts and is unfalsifiable. This derives it by calling
the real binary over stdio -- the same path evals/tokens/sweep.py uses -- and
stores the generating query beside the answer, so any reader can reproduce it.

Only `set` and `scalar` expectations are generated. `prose` cases are left for the
judge, per evals/docs/design.md: a judge earns its place where correctness means
faithfulness rather than accuracy.

    python -m judge.groundtruth              # generate, write back
    python -m judge.groundtruth --check      # verify nothing drifted, write nothing
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
from datetime import UTC, datetime
from pathlib import Path

from common.client import mcp_session, text_of
from common.config import get_settings, graphql_sha, harness_version, tool_server_sha
from judge.models import Case, Generated, Manifest, ScalarExpected, SetExpected
from judge.payload import GroundTruthError, extract, fingerprint
from tokens.corpus import args_sha

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

JUDGE_ROOT = Path(__file__).parent
DATA = JUDGE_ROOT / "data"
GOLDEN_SET = DATA / "golden-set.jsonl"
MANIFEST = DATA / "manifest.json"


def load_cases(path: Path = GOLDEN_SET) -> list[Case]:
    """Cases in file order. JSONL, so a bad line names itself."""
    cases: list[Case] = []
    for lineno, line in enumerate(path.read_text().splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        try:
            cases.append(Case.model_validate_json(line))
        except ValueError as exc:
            raise GroundTruthError(f"{path}:{lineno}: {exc}") from exc

    ids = [c.case_id for c in cases]
    if len(set(ids)) != len(ids):
        dupes = sorted({i for i in ids if ids.count(i) > 1})
        raise GroundTruthError(f"{path}: duplicate case_id {dupes}")
    return cases


async def generate(cases: list[Case]) -> dict[str, str]:
    """Fill in each deterministic case's Generated block. Returns the fingerprints."""
    prints: dict[str, str] = {}
    generable = [c for c in cases if isinstance(c.expected, SetExpected | ScalarExpected)]
    logger.info("generating %d of %d cases; the rest are prose", len(generable), len(cases))

    # Passed explicitly rather than left to the subprocess's own default, so the
    # endpoint the manifest records is the endpoint the calls actually went to.
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

            # An empty set is never a useful happy_path expectation: the model
            # would pass by saying nothing was found, whatever the response shape.
            # It means the case is pointed at a place the snapshot does not cover.
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


def write(cases: list[Case], prints: dict[str, str]) -> None:
    settings = get_settings()
    with GOLDEN_SET.open("w") as handle:
        for case in cases:
            handle.write(case.model_dump_json(exclude_none=True) + "\n")

    # Built rather than copied over the previous one: every field is written on
    # every run, so there is nothing to carry forward, and constructing it here is
    # what lets the model require them all.
    manifest = Manifest(
        harness_version=harness_version(),
        tool_server_sha=tool_server_sha(),
        openbeta_graphql_sha=graphql_sha(),
        snapshot_date=datetime.now(UTC).date().isoformat(),
        endpoint=settings.openbeta_endpoint,
        max_crags=settings.openbeta_max_crags,
        generated_at=datetime.now(UTC).isoformat(),
        fingerprint=prints,
    )
    MANIFEST.write_text(manifest.model_dump_json(indent=2) + "\n")
    logger.info("wrote %d cases and %d fingerprints", len(cases), len(prints))


def check(prints: dict[str, str]) -> int:
    """Compare fresh fingerprints against the manifest. Exit code is the verdict."""
    recorded = Manifest.model_validate_json(MANIFEST.read_text()).fingerprint
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
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="verify against the manifest, write nothing")
    args = parser.parse_args()

    logger.info("endpoint %s", get_settings().openbeta_endpoint)
    cases = load_cases()
    prints = asyncio.run(generate(cases))

    if args.check:
        return check(prints)
    write(cases, prints)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
