"""The deterministic checks. No server, no model, nothing mocked.

Several fixtures are real failures found when the grader first ran against the
committed rows, kept so they cannot come back.
"""

from __future__ import annotations

import json

import pytest

from judge.grade import Grader
from judge.grading import mentions, names_in, normalise, prf, routes_offered
from judge.models import Case
from judge.runner import Result, ToolCall, Usage

CASE = {
    "case_id": "t",
    "capability": "c",
    "category": "happy_path",
    "user_input": "q",
    "criteria": ["says something true"],
}


def case(**over) -> Case:
    return Case.model_validate(CASE | over)


def result(answer: str, calls: list[ToolCall] | None = None, **over) -> Result:
    return Result(
        run_id="r",
        case_id="t",
        capability="c",
        category="happy_path",
        model="m",
        tools_enabled=True,
        attempt=1,
        harness_version="0.1.0",
        tool_server_sha="a" * 40,
        endpoint="http://x/",
        max_crags=20,
        answer=answer,
        tool_calls=calls or [],
        turns=1,
        stop_reason="end_turn",
        usage=Usage(),
        ms=1,
        **over,
    )


def set_case(*expected: str) -> Case:
    """A set case expecting these route names."""
    return case(
        expected={
            "kind": "set",
            "query": {"tool": "find_climbs", "args": {"place": "Rumney"}},
            "generated": {"value": list(expected), "args_sha": "x"},
        }
    )


def tool_call(payload: object) -> ToolCall:
    text = payload if isinstance(payload, str) else json.dumps(payload)
    return ToolCall(turn=1, name="find_climbs", args={}, args_sha="x", result=text, is_error=False)


@pytest.mark.parametrize(
    ("phrase", "answer", "expected"),
    [
        pytest.param("star", "- **Gettin' Started** (Upper Right)", False, id="star inside Started"),
        pytest.param("-pitch route", "several multi-pitch routes", False, id="-pitch route inside multi-pitch"),
        pytest.param("most climbed", "the most climbed routes here", True, id="a real violation"),
        pytest.param("approach", "The approach is short", True, id="plain word"),
        pytest.param("Star", "a star rating", True, id="case-insensitive"),
    ],
)
def test_mentions_uses_word_boundaries(phrase, answer, expected):
    """Naive substring matching gave three false positives out of four on the
    first real run. Each is pinned here."""
    assert mentions(answer, phrase) is expected


def test_normalise_strips_a_leading_article():
    assert normalise("The Smoke Bluffs") == normalise("Smoke Bluffs")
    assert normalise("  CAFE au Lait ") == "cafe au lait"


def test_names_in_finds_the_named_routes():
    found = names_in("I'd try **Cafe au Lait** or the Rose Garden.", {"Cafe au Lait", "Rose Garden", "Sex Ed"})

    assert found == {"Cafe au Lait", "Rose Garden"}


def test_routes_offered_reads_both_tool_shapes():
    """crags_near returns `crags`, find_climbs returns `climbs`; both are names."""
    calls = [
        tool_call({"climbs": [{"name": "Cafe au Lait"}, {"name": "Sex Ed"}]}),
        tool_call({"crags": [{"name": "Smoke Bluffs"}]}),
    ]

    assert routes_offered([c.result for c in calls]) == {"Cafe au Lait", "Sex Ed", "Smoke Bluffs"}


def test_routes_offered_survives_a_failed_call():
    """A failed tool call returns prose, which trad_first_lead_gunks actually hit."""
    calls = [
        tool_call('no coordinates known for "Gunks"; pass lnglat instead'),
        tool_call({"climbs": [{"name": "Jane"}]}),
    ]

    assert routes_offered([c.result for c in calls]) == {"Jane"}


@pytest.mark.parametrize(
    ("named", "seen", "expected", "precision", "recall"),
    [
        pytest.param({"Jane", "Clover"}, {"Jane", "Clover", "Laurel"}, {"Jane"}, 1.0, 1.0, id="all returned"),
        pytest.param({"Jane", "Ghost"}, {"Jane"}, {"Jane"}, 0.5, 1.0, id="one name came from elsewhere"),
        pytest.param({"Jane"}, {"Jane", "Clover"}, {"Jane", "Clover"}, 1.0, 0.5, id="a curated subset"),
        pytest.param(set(), {"Jane"}, {"Jane"}, 1.0, 0.0, id="a refusal invents nothing"),
        pytest.param({"the smoke bluffs"}, {"The Smoke Bluffs"}, {"The Smoke Bluffs"}, 1.0, 1.0, id="normalised"),
    ],
)
def test_prf(named, seen, expected, precision, recall):
    """Precision is what the gate reads; recall separates summarising from dropping."""
    p, r, _ = prf(named=named, seen=seen, expected=expected)

    assert (p, r) == (precision, recall)


def test_grades_a_set_case_on_precision():
    calls = [tool_call({"climbs": [{"name": "Jane"}, {"name": "Clover"}]})]
    grade = Grader({"t": set_case("Jane", "Clover")}).grade(result("Try **Jane**.", calls))

    assert grade.graded and grade.passed
    assert (grade.precision, grade.recall) == (1.0, 0.5)


def test_naming_an_expected_route_the_tools_did_not_return_fails_the_gate():
    """The fabrication precision catches: a real route, not from this run's output.

    Typically a name recalled from training data. The case expects Jane and
    Clover; the tools returned only Jane, so naming Clover is unsupported.
    """
    calls = [tool_call({"climbs": [{"name": "Jane"}]})]
    grade = Grader({"t": set_case("Jane", "Clover")}).grade(result("Try **Jane** or **Clover**.", calls))

    assert grade.precision == 0.5
    assert grade.passed is False


def test_a_wholly_invented_name_is_not_detected():
    """A known limitation, pinned so it is a decision rather than a surprise.

    Precision searches the names the case or the tools know about, so a route
    invented out of nothing is never a candidate. Catching that needs a name
    extractor, and bold spans -- the obvious signal -- are mostly headings and
    advice in the real answers. The judge covers this on prose cases.
    """
    calls = [tool_call({"climbs": [{"name": "Jane"}]})]
    grade = Grader({"t": set_case("Jane")}).grade(result("Try **Jane** or **Entirely Made Up**.", calls))

    assert grade.precision == 1.0


def test_a_zero_scalar_may_be_reported_in_prose():
    """ "There's no climbing there" is the right answer; demanding the digit is not."""
    c = case(
        expected={
            "kind": "scalar",
            "query": {"tool": "crags_near", "args": {"lnglat": [-140.0, 35.0]}},
            "generated": {"value": 0, "args_sha": "x"},
        }
    )
    grade = Grader({"t": c}).grade(result("No climbing is recorded near those coordinates."))

    assert grade.scalar_ok and grade.passed


def test_a_forbidden_phrase_fails():
    c = case(
        expected={"kind": "prose", "why_not_deterministic": "hedging"},
        must_not_include=["most climbed"],
    )
    grade = Grader({"t": c}).grade(result("These are the most climbed routes."))

    assert grade.forbidden_phrases == ["most climbed"]
    assert grade.passed is False


def test_a_prose_case_with_no_phrases_is_left_for_the_judge():
    c = case(expected={"kind": "prose", "why_not_deterministic": "graded on behaviour"})
    grade = Grader({"t": c}).grade(result("Some prose."))

    assert grade.graded is False
    assert grade.passed is None


def test_an_errored_row_is_not_counted_as_a_failure():
    """A 529 from the API is not the model getting the answer wrong."""
    grade = Grader({"t": set_case("Jane")}).grade(result("", error="InternalServerError: overloaded"))

    assert grade.graded is False
    assert grade.passed is None
