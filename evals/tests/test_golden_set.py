"""The golden set and the models that validate it. No server, nothing mocked.

The committed set is read from disk, so these fail if a case is edited into an
invalid state — which is the point, since a malformed case would otherwise
surface as a confusing tool error partway through a paid sweep.
"""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from judge.groundtruth import load_cases
from judge.models import TOOL_ARGS, Case, Manifest
from judge.payload import extract, fingerprint

CASE = {
    "case_id": "t",
    "capability": "c",
    "category": "edge",
    "user_input": "q",
    "expected": {"kind": "prose", "why_not_deterministic": "graded on behaviour"},
    "criteria": ["says something true"],
}

MANIFEST = {
    "harness_version": "0.1.0",
    "tool_server_sha": "a" * 40,
    "openbeta_graphql_sha": "b" * 40,
    "snapshot_date": "2026-08-29",
    "endpoint": "http://localhost:4000/",
    "max_crags": 20,
    "generated_at": "2026-08-29T00:00:00Z",
    "fingerprint": {"a_case": "deadbeef"},
}


def test_the_committed_set_is_valid():
    cases = load_cases()

    assert len(cases) == 27
    assert len({c.case_id for c in cases}) == len(cases)


def test_every_deterministic_case_has_been_generated():
    """An ungenerated case would grade against None."""
    for case in load_cases():
        if case.expected.kind in ("set", "scalar"):
            assert case.expected.generated, case.case_id
            assert case.expected.generated.args_sha, case.case_id


def test_every_query_uses_real_tool_parameters():
    """The defect that made the previous golden set ungradeable."""
    for case in load_cases():
        if case.expected.kind == "prose":
            continue
        query = case.expected.query
        assert set(query.args) <= TOOL_ARGS[query.tool], case.case_id


@pytest.mark.parametrize(
    "change",
    [
        pytest.param({"expected_tools": ["get_ticks"], "allowed_tools": ["get_ticks"]}, id="tool does not exist"),
        pytest.param({"expected_tools": ["find_climbs"], "allowed_tools": []}, id="expected tool not allowed"),
        pytest.param({"place": "Squamish"}, id="place outside the USA"),
        pytest.param({"place": "Llanberis"}, id="place not in the gazetteer"),
        pytest.param({"category": "made_up"}, id="unknown category"),
        pytest.param({"difficulty": "hard"}, id="field that no longer exists"),
        pytest.param({"criteria": []}, id="no criteria"),
    ],
)
def test_rejects_a_malformed_case(change):
    with pytest.raises(ValidationError):
        Case.model_validate(CASE | change)


@pytest.mark.parametrize(
    "expected",
    [
        pytest.param({"kind": "set", "query": {"tool": "find_climbs", "args": {"style": "sport"}}}, id="no such param"),
        pytest.param({"kind": "set", "query": {"tool": "search_routes", "args": {}}}, id="no such tool"),
        pytest.param({"kind": "set"}, id="set with nothing to generate from"),
        pytest.param({"kind": "prose"}, id="prose not saying why"),
    ],
)
def test_rejects_a_malformed_expectation(expected):
    with pytest.raises(ValidationError):
        Case.model_validate(CASE | {"expected": expected})


def test_manifest_accepts_a_complete_record():
    assert Manifest.model_validate(MANIFEST)


@pytest.mark.parametrize("missing", list(MANIFEST))
def test_manifest_requires_every_field(missing):
    """A run pinned to nothing cannot be compared with a later one."""
    with pytest.raises(ValidationError):
        Manifest.model_validate({k: v for k, v in MANIFEST.items() if k != missing})


def test_manifest_refuses_a_model():
    """The model under test belongs in the result row, not the manifest."""
    with pytest.raises(ValidationError):
        Manifest.model_validate(MANIFEST | {"model": "claude-haiku-4-5"})


def test_extract_sorts_and_deduplicates_names():
    payload = {"climbs": [{"name": "B"}, {"name": "A"}, {"name": "A"}]}

    assert extract("find_climbs", "set", payload) == ["A", "B"]


def test_extract_takes_the_honest_scalar():
    """crags_near reports `returned`; `count` includes crags holding nothing."""
    payload = {"count": 500, "returned": 20, "crags": []}

    assert extract("crags_near", "scalar", payload) == 20


def test_fingerprint_ignores_order_but_not_content():
    one = {"climbs": [{"uuid": "1"}, {"uuid": "2"}]}
    reordered = {"climbs": [{"uuid": "2"}, {"uuid": "1"}]}
    different = {"climbs": [{"uuid": "1"}, {"uuid": "3"}]}

    assert fingerprint("find_climbs", one) == fingerprint("find_climbs", reordered)
    assert fingerprint("find_climbs", one) != fingerprint("find_climbs", different)


def test_no_fingerprint_for_an_empty_result():
    """The ocean and no-coverage cases return nothing, so there is nothing to hash."""
    assert fingerprint("crags_near", {"crags": []}) is None
