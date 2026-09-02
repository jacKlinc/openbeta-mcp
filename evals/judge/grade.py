"""Score result rows against the golden set. See judge/README.md."""

from __future__ import annotations

import argparse
import logging
import sys
from collections.abc import Iterable
from pathlib import Path

from common import jsonl
from judge.dataset import GRADES, RUNS, GoldenSet
from judge.grading import mentions, names_in, prf, reports_empty, routes_offered
from judge.models import Case, Grade
from judge.runner import Result

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class Grader:
    """Scores result rows against the golden set.

    Args:
        cases: by case_id. A dict rather than a GoldenSet so a test can pass two
            cases without building a fixture directory.
    """

    def __init__(self, cases: dict[str, Case]) -> None:
        self.cases = cases

    def grade(self, result: Result) -> Grade:
        """Score one row.

        Raises:
            KeyError: the row names a case that is not in the set.
        """
        case = self.cases[result.case_id]
        grade = Grade(
            run_id=result.run_id,
            case_id=result.case_id,
            attempt=result.attempt,
            category=case.category,
            tools_enabled=result.tools_enabled,
            kind=case.expected.kind,
            graded=False,
        )

        # An errored row has no answer to score; leaving it ungraded keeps a
        # failed API call out of the pass rate rather than counting as a failure.
        if result.error:
            return grade

        self._score_phrases(case, result, grade)

        if case.expected.kind == "set" and case.expected.generated:
            expected = set(case.expected.generated.value)
            seen = routes_offered([c.result for c in result.tool_calls])
            named = names_in(result.answer, expected | seen)
            grade.precision, grade.recall, grade.f1 = prf(named, seen, expected)
            grade.graded = True

        elif case.expected.kind == "scalar" and case.expected.generated:
            expected_value = case.expected.generated.value
            # Zero is nearly always reported in prose rather than as a digit.
            grade.scalar_ok = (
                reports_empty(result.answer) if expected_value == 0 else mentions(result.answer, str(expected_value))
            )
            grade.graded = True

        if grade.graded:
            grade.passed = (
                not grade.forbidden_phrases
                and not grade.missing_phrases
                and (grade.precision is None or grade.precision == 1.0)
                and (grade.scalar_ok is not False)
            )
        return grade

    def _score_phrases(self, case: Case, result: Result, grade: Grade) -> None:
        """Check must_include and must_not_include, marking the row graded if either applies."""
        grade.missing_phrases = [p for p in case.must_include if not mentions(result.answer, p)]
        grade.forbidden_phrases = [p for p in case.must_not_include if mentions(result.answer, p)]
        if case.must_include or case.must_not_include:
            grade.graded = True

    def grade_all(self, results: Iterable[Result]) -> list[Grade]:
        """Score every row. Returns rows; writing them is the caller's job."""
        return [self.grade(r) for r in results]


def summarise(grades: list[Grade]) -> None:
    """Log the pass rate and every failure, so a bad run names itself."""
    graded = [g for g in grades if g.graded]
    passed = [g for g in graded if g.passed]
    logger.info("%d of %d rows graded, %d passed", len(graded), len(grades), len(passed))

    for g in graded:
        if g.passed:
            continue
        why = []
        if g.forbidden_phrases:
            why.append(f"said {g.forbidden_phrases}")
        if g.missing_phrases:
            why.append(f"omitted {g.missing_phrases}")
        if g.precision is not None and g.precision < 1.0:
            why.append(f"precision {g.precision:.2f}")
        if g.scalar_ok is False:
            why.append("wrong scalar")
        logger.info("  FAIL %s (tools=%s): %s", g.case_id, g.tools_enabled, "; ".join(why))


def main() -> int:
    """Grade a runs file and write the grades beside it."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("runs", type=Path, nargs="?", default=RUNS, help="runs.jsonl to grade")
    args = parser.parse_args()

    if not args.runs.exists():
        logger.error("%s does not exist; run the sweep first", args.runs)
        return 1

    df, malformed = jsonl.read_df(args.runs)
    if malformed:
        logger.warning("%s: %d malformed lines skipped", args.runs, malformed)

    results = [Result.model_validate(row) for row in df.to_dict("records")]
    grader = Grader({c.case_id: c for c in GoldenSet().cases()})
    grades = grader.grade_all(results)

    GRADES.unlink(missing_ok=True)
    for grade in grades:
        jsonl.append(GRADES, grade.model_dump())

    summarise(grades)
    logger.info("wrote %s", GRADES)
    return 0


if __name__ == "__main__":
    sys.exit(main())
