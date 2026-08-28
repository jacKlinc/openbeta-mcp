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
import hashlib
import json
import logging
import subprocess
import tomllib
from datetime import UTC, datetime
from functools import cache
from pathlib import Path
from typing import Annotated, Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from common.client import mcp_session, text_of
from tokens.corpus import US_PLACES, args_sha, places

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

JUDGE_ROOT = Path(__file__).parent
DATA = JUDGE_ROOT / "data"
GOLDEN_SET = DATA / "golden-set.jsonl"
MANIFEST = DATA / "manifest.json"

SCHEMA_VERSION = 1


class Settings(BaseSettings):
    """Environment the harness reads, declared in one place.

    These were scattered across os.environ lookups with silent defaults, which is
    how a run ends up recording an endpoint it did not use. Declared here they are
    typed, defaulted once, and visible in the manifest.
    """

    model_config = SettingsConfigDict(
        env_prefix="",
        extra="ignore",
        # The repo .env is what scripts/ and the server itself read. Loading it
        # here means the manifest records the endpoint actually in use rather than
        # the default, which is the whole point of recording it. A real environment
        # variable still wins over the file.
        env_file=Path(__file__).parents[2] / ".env",
    )

    # Local by default, unlike the server's own default of api.openbeta.io. The
    # expectations in this set are pinned to the seeded local snapshot, so falling
    # back to live would regenerate them against different data and record a
    # fingerprint that then fails every subsequent --check.
    openbeta_endpoint: str = "http://localhost:4000/"
    # Matches MaxCrags in internal/tools/crags_near.go. Set explicitly rather than
    # left unset, so the manifest records the cap that was actually in force --
    # cost-vs-quality per cap is meant to be a join, and a null does not join.
    openbeta_max_crags: int = 20
    graphql_dir: Path = Path.home() / "repos/openbeta/openbeta-graphql"

    @field_validator("openbeta_endpoint")
    @classmethod
    def endpoint_has_a_scheme(cls, v: str) -> str:
        """A bare host:port reaches the GraphQL client as a relative URL.

        It fails deep inside the transport with an error that names neither the
        setting nor the value, so catch it here where the message can say both.
        """
        if not v.startswith(("http://", "https://")):
            raise ValueError(f"{v!r} needs a scheme, e.g. http://{v}")
        return v


@cache
def get_settings() -> Settings:
    """Settings, resolved on first use.

    Deliberately not a module-level instance: a bad OPENBETA_ENDPOINT in .env would
    then fail at import, taking down offline uses of load_cases that never touch the
    network. Resolved lazily, the error appears when the endpoint is actually needed.
    """
    return Settings()


Tool = Literal["crags_near", "find_climbs", "get_area_details"]

# Every field of every tool's input schema, from internal/tools/. A case naming
# anything else is asserting against a parameter that does not exist, which was
# the single most common defect in the set this file replaced.
TOOL_ARGS: dict[str, set[str]] = {
    "crags_near": {"place", "lnglat", "maxDistanceKm"},
    "find_climbs": {"place", "lnglat", "maxDistanceKm", "disciplines", "minGrade", "maxGrade", "multipitchOnly"},
    "get_area_details": {"areaId"},
}

# The list of records a response carries, per tool. One lookup serves both the
# expected value and the fingerprint, since they differ only in which key they
# read off each record.
ITEMS: dict[str, Any] = {
    "crags_near": lambda p: p.get("crags", []),
    "find_climbs": lambda p: p.get("climbs", []),
    "get_area_details": lambda p: p.get("area", {}).get("climbs", []),
}

# The scalar each tool reports. crags_near returns `returned` rather than `count`
# because count is an upper bound that includes crags holding nothing.
SCALARS: dict[str, Any] = {
    "crags_near": lambda p: p.get("returned", 0),
    "find_climbs": lambda p: p.get("count", 0),
    "get_area_details": lambda p: len(p.get("area", {}).get("climbs", [])),
}


class GroundTruthError(RuntimeError):
    """A case cannot be generated truthfully. Never downgraded to a warning."""


class Query(BaseModel):
    """The tool call that produces a case's expected value."""

    model_config = ConfigDict(extra="forbid")

    tool: Tool
    args: dict[str, Any] = Field(default_factory=dict)

    @model_validator(mode="after")
    def args_exist_on_tool(self) -> Self:
        unknown = set(self.args) - TOOL_ARGS[self.tool]
        if unknown:
            raise ValueError(f"{self.tool} has no parameter {sorted(unknown)}")
        return self


class Generated(BaseModel):
    """The answer a query produced, and the query's identity in the cost runs.

    Present together or not at all: a case is either generated or it is not, and
    the two fields were nullable only to express the gap between them.
    """

    model_config = ConfigDict(extra="forbid")

    value: list[str] | int
    args_sha: str


class SetExpected(BaseModel):
    """A list of route or area names, graded by set F1."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["set"] = "set"
    query: Query
    generated: Generated | None = None


class ScalarExpected(BaseModel):
    """A single number, graded by exact match."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["scalar"] = "scalar"
    query: Query
    generated: Generated | None = None


class ProseExpected(BaseModel):
    """Graded by the judge on groundedness, so it carries no value to compare."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["prose"] = "prose"
    why_not_deterministic: str


Expected = Annotated[SetExpected | ScalarExpected | ProseExpected, Field(discriminator="kind")]


class Case(BaseModel):
    """One golden-set case.

    Validation that used to be a hand-rolled loop lives in the model, so a
    malformed case fails at load with the offending field named, rather than as a
    confusing tool error halfway through a sweep.
    """

    model_config = ConfigDict(extra="forbid")

    case_id: str
    capability: str
    category: Literal["happy_path", "edge", "no_data", "honest_failure", "no_tool_baseline"]
    user_input: str
    place: str | None = None
    grade_range: str | None = None
    pitch_filter: Literal["multipitch", "any"] = "any"
    expected: Expected
    requires_fields: list[str] = Field(default_factory=list)
    expected_tools: list[Tool] = Field(default_factory=list)
    allowed_tools: list[Tool] = Field(default_factory=list)
    must_include: list[str] = Field(default_factory=list)
    must_not_include: list[str] = Field(default_factory=list)
    criteria: Annotated[list[str], Field(min_length=1)]
    tags: list[str] = Field(default_factory=list)

    @model_validator(mode="after")
    def expected_tools_are_allowed(self) -> Self:
        extra = set(self.expected_tools) - set(self.allowed_tools)
        if extra:
            raise ValueError(f"expected_tools {sorted(extra)} are not in allowed_tools")
        return self

    @model_validator(mode="after")
    def place_is_a_usa_gazetteer_entry(self) -> Self:
        """The set is USA-only, deliberately.

        Two independent reasons, either sufficient. The seeded local stack holds
        USA areas only -- Squamish, Peak District, Canmore and Skaha all return
        zero crags against it -- so a non-USA case would grade the model on an
        empty result. And Area.totalClimbs undercounts badly outside the USA
        (docs/findings/totalclimbs/: 88% in British Columbia, 98% in Alberta),
        which corrupts both the ordering and the membership of any crags_near
        result, since it sorts by climb count and caps at 20.

        There is enough USA data to cover every capability worth testing, so the
        set stays inside the cohort where the numbers are sound. Verified: 228 of
        228 Yosemite Valley leaves report totalClimbs == len(climbs).
        """
        if self.place is None:
            return self
        if self.place not in set(places()):
            raise ValueError(f"place {self.place!r} is not in the gazetteer")
        if self.place not in US_PLACES:
            raise ValueError(
                f"place {self.place!r} is outside the USA. The local stack holds no data for it and "
                "totalClimbs is unreliable there; see docs/findings/totalclimbs/"
            )
        return self


class Manifest(BaseModel):
    """What the expectations are pinned to.

    Written by this module; read by anything that wants to know whether a score is
    comparable with an earlier one.

    No `model` field, deliberately: the model under test belongs in the result row
    keyed on (run_id, case_id), alongside judge_model -- see docs/plans/judge.md.
    design.md runs the same cases across two model sizes and pairs the comparison,
    so a model pinned here would fork the set and cost the paired tests their power.
    """

    model_config = ConfigDict(extra="forbid")

    schema_version: int = SCHEMA_VERSION
    harness_version: str
    tool_server_sha: str
    openbeta_graphql_sha: str
    snapshot_date: str
    endpoint: str
    max_crags: int
    generated_at: str
    fingerprint: Annotated[dict[str, str], Field(min_length=1)]

    # TODO: required once a rubric exists. Nullable only because there is none to
    # version yet, not because a run may legitimately lack one.
    rubric_version: str | None = None


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


def git_sha(repo: Path) -> str:
    """HEAD of a git checkout.

    Raises rather than returning None: an unrecorded sha means the expectations
    are pinned to nothing, which defeats the only purpose the manifest has.
    """
    if not (repo / ".git").exists():
        raise GroundTruthError(f"no git checkout at {repo}, so provenance cannot be recorded")
    try:
        out = subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        raise GroundTruthError(f"git rev-parse failed in {repo}: {(exc.stderr or '').strip()}") from exc
    except OSError as exc:
        raise GroundTruthError(f"could not run git in {repo}: {exc}") from exc

    sha = out.stdout.strip()
    if not sha:
        raise GroundTruthError(f"git rev-parse returned nothing in {repo}")
    return sha


def harness_version() -> str:
    """Declared version of this harness, from pyproject.toml.

    Read from the file rather than importlib.metadata because evals is a uv
    virtual project with no build backend, so nothing ever installs it and the
    metadata does not exist. Complements tool_server_sha rather than duplicating
    it: the sha pins the exact code, this records the version someone bumped.
    """
    pyproject = Path(__file__).parents[1] / "pyproject.toml"
    try:
        return tomllib.loads(pyproject.read_text())["project"]["version"]
    except (OSError, tomllib.TOMLDecodeError, KeyError) as exc:
        raise GroundTruthError(f"could not read a version from {pyproject}: {exc}") from exc


def extract(tool: str, kind: str, payload: dict[str, Any]) -> list[str] | int:
    """The expected value, pulled from a tool response.

    Names rather than uuids for sets: design.md grades these with normalised string
    matching, and a uuid tells a reader nothing when a case is reviewed by hand.
    """
    if kind == "set":
        return sorted({item["name"] for item in ITEMS[tool](payload)})
    if kind == "scalar":
        return SCALARS[tool](payload)
    raise GroundTruthError(f"no extraction rule for kind={kind!r}")


def fingerprint(tool: str, payload: dict[str, Any]) -> str | None:
    """sha256 over the uuids a query returned, for drift detection.

    Over uuids, never over climbCount: docs/findings/totalclimbs/ measured that
    field as unreliable, so hashing it would report drift that is not real and
    stay quiet on drift that is.
    """
    uuids = sorted(item["uuid"] for item in ITEMS[tool](payload))
    if not uuids:
        return None
    return hashlib.sha256("".join(uuids).encode()).hexdigest()[:16]


async def generate(cases: list[Case]) -> dict[str, str]:
    """Fill in each deterministic case's Generated block. Returns the fingerprints."""
    prints: dict[str, str] = {}
    generable = [c for c in cases if isinstance(c.expected, SetExpected | ScalarExpected)]
    logger.info("generating %d of %d cases; the rest are prose", len(generable), len(cases))

    # Passed explicitly rather than left to the subprocess's own default, so the
    # endpoint the manifest records is the endpoint the calls actually went to.
    settings = get_settings()
    env = {
        "OPENBETA_ENDPOINT": settings.openbeta_endpoint,
        "OPENBETA_MAX_CRAGS": str(settings.openbeta_max_crags),
    }

    async with mcp_session(env=env) as session:
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
        tool_server_sha=git_sha(Path(__file__).parents[2]),
        openbeta_graphql_sha=git_sha(settings.graphql_dir),
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
