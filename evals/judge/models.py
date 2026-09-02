"""The golden set's schema. See judge/README.md."""

from __future__ import annotations

from enum import StrEnum
from typing import Annotated, Any, Literal, Self

from pydantic import BaseModel, ConfigDict, Field, model_validator

from tokens.corpus import US_PLACES, places

SCHEMA_VERSION = 1

Tool = Literal["crags_near", "find_climbs", "get_area_details"]


class Category(StrEnum):
    """What kind of answer a case is testing for."""

    HAPPY_PATH = "happy_path"
    """The tool can answer and the answer is checkable."""

    EDGE = "edge"
    """The tool answers, but the honest answer is not the obvious one: a capped
    count, contradictory constraints, a scope limit."""

    NO_DATA = "no_data"
    """The query is well formed and the answer is genuinely empty. Grades whether
    the agent reports that or fills the gap."""

    HONEST_FAILURE = "honest_failure"
    """The data cannot support the question, so the correct answer says so. The
    real failure mode of a trimmed response is not that the model cannot answer,
    it is that it invents something plausible."""

    NO_TOOL_BASELINE = "no_tool_baseline"
    """Answerable from the model's own knowledge. The number the server has to
    beat: if it does not, it is not earning its tokens."""


# Every field of every tool's input schema, from internal/tools/. A case naming
# anything else asserts against a parameter that does not exist, which was the
# single most common defect in the set this replaced.
TOOL_ARGS: dict[str, set[str]] = {
    "crags_near": {"place", "lnglat", "maxDistanceKm"},
    "find_climbs": {"place", "lnglat", "maxDistanceKm", "disciplines", "minGrade", "maxGrade", "multipitchOnly"},
    "get_area_details": {"areaId"},
}


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
    """What a query returned. Present together or not at all."""

    model_config = ConfigDict(extra="forbid")

    value: list[str] | int = Field(description="Route or area names for a set, a count for a scalar.")
    args_sha: str = Field(description="Joins this case to the cost dataset on (tool, args_sha).")


class SetExpected(BaseModel):
    """A list of route or area names, graded by set F1."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["set"] = "set"
    query: Query
    generated: Generated | None = Field(default=None, description="Written by judge.groundtruth; null until then.")


class ScalarExpected(BaseModel):
    """A single number, graded by exact match."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["scalar"] = "scalar"
    query: Query
    generated: Generated | None = Field(default=None, description="Written by judge.groundtruth; null until then.")


class ProseExpected(BaseModel):
    """Graded by the judge on groundedness, so it carries no value to compare."""

    model_config = ConfigDict(extra="forbid")

    kind: Literal["prose"] = "prose"
    why_not_deterministic: str = Field(description="Why this cannot be machine-graded. Keeps the judge a last resort.")


Expected = Annotated[SetExpected | ScalarExpected | ProseExpected, Field(discriminator="kind")]


class Case(BaseModel):
    """One golden-set case."""

    model_config = ConfigDict(extra="forbid")

    case_id: str
    capability: str = Field(description="The one thing under test; the axis results are sliced on.")
    category: Category
    user_input: str
    place: str | None = Field(default=None, description="Gazetteer place, USA-only. Null for clarification cases.")
    grade_range: str | None = None
    pitch_filter: Literal["multipitch", "any"] = "any"
    expected: Expected
    requires_fields: list[str] = Field(
        default_factory=list,
        description="Response paths the answer depends on, so a variant's failure is predicted not observed.",
    )
    expected_tools: list[Tool] = Field(default_factory=list, description="Tools a correct answer calls.")
    allowed_tools: list[Tool] = Field(
        default_factory=list, description="Tools that may be called. Anything outside this is a failure."
    )
    must_include: list[str] = Field(default_factory=list, description="Substrings the answer needs.")
    must_not_include: list[str] = Field(default_factory=list, description="Fabrications this case exists to catch.")
    criteria: list[str] = Field(min_length=1, description="Per-case points fed to the one shared rubric.")
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
        USA areas only, so a non-USA case would grade an empty result. And
        Area.totalClimbs undercounts badly outside the USA, which corrupts
        crags_near ordering and membership. See docs/findings/totalclimbs/.
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

    No `model` field: the model under test belongs in the result row, keyed on
    (run_id, case_id). design.md runs the same cases across two model sizes and
    pairs the comparison, so a model pinned here would fork the set.
    """

    model_config = ConfigDict(extra="forbid")

    schema_version: Annotated[
        int,
        Field(description="The case file format. Bump when a field is added, removed or reinterpreted."),
    ] = SCHEMA_VERSION
    harness_version: Annotated[str, Field(description="Declared version of the evals package.")]
    tool_server_sha: Annotated[str, Field(description="Commit of the Go server the expectations came from.")]
    openbeta_graphql_sha: Annotated[str, Field(description="Commit of the openbeta-graphql checkout serving them.")]
    snapshot_date: str
    endpoint: Annotated[str, Field(description="The endpoint the calls actually went to.")]
    max_crags: Annotated[int, Field(description="The cap in force, so cost against quality per cap is a join.")]
    generated_at: str
    fingerprint: Annotated[
        dict[str, str],
        Field(min_length=1, description="Per case, a hash of the uuids returned. Detects snapshot rot."),
    ]

    # TODO: required once a rubric exists. Nullable only because there is none to
    # version yet, not because a run may legitimately lack one.
    rubric_version: str | None = None
