"""The deterministic checks, as pure functions.

Strings and sets in, numbers out. No case, no state, no IO, so the parts that are
actually tricky -- word boundaries, non-JSON tool results, name normalisation --
test without constructing anything.
"""

from __future__ import annotations

import json
import re

from judge.payload import ITEMS

# Only a leading article. Stripping more would collapse distinct routes.
ARTICLE = re.compile(r"^(the|a|an)\s+", re.I)


def normalise(name: str) -> str:
    """Case-fold and drop a leading article, so "The Smoke Bluffs" == "Smoke Bluffs"."""
    return ARTICLE.sub("", name.strip()).casefold()


def mentions(text: str, phrase: str) -> bool:
    """Whether the phrase appears as whole words.

    Word-bounded because a plain substring test is unusable here: "star" matches
    "Gettin' Started" and "-pitch route" matches "multi-pitch routes", which
    produced three false positives out of four on the first real run.
    """
    return bool(re.search(rf"\b{re.escape(phrase)}\b", text, re.I))


def names_in(text: str, candidates: set[str]) -> set[str]:
    """The candidates the text names, matched on the normalised form.

    Returns:
        The original spellings, so a caller can report what it found.
    """
    folded = normalise(text)
    return {c for c in candidates if normalise(c) in folded}


def routes_offered(results: list[str]) -> set[str]:
    """Every route or crag name the tools returned across one case.

    Args:
        results: the raw `ToolCall.result` texts, in call order.

    A result that is not JSON is skipped rather than raised on: a failed call
    returns prose, such as `no coordinates known for "Gunks"`.
    """
    names: set[str] = set()
    for text in results:
        try:
            payload = json.loads(text)
        except (json.JSONDecodeError, TypeError):
            continue
        if not isinstance(payload, dict):
            continue
        for items in ITEMS.values():
            names |= {i["name"] for i in items(payload) if isinstance(i, dict) and "name" in i}
    return names


def prf(named: set[str], seen: set[str], expected: set[str]) -> tuple[float, float, float]:
    """Precision, recall and F1 for one answer.

    Args:
        named: names the model wrote in its answer.
        seen: names its tools actually returned. Precision is measured against
            this, not against `expected`, so a model that chose different but
            valid arguments is not scored as fabricating.
        expected: names the case's pinned query returns; the retrieval reference.

    Precision only sees names the case or the tools already know about, so a route
    invented out of nothing is invisible here -- catching that needs a name
    extractor, and the obvious signal (bold spans) is mostly headings in practice.
    The judge covers it on prose cases.

    Returns:
        (precision, recall, f1). Precision is 1.0 for an answer naming nothing --
        it invented nothing -- while recall is 0.0, which is what separates a
        refusal from a fabrication.
    """
    folded_seen = {normalise(n) for n in seen}
    folded_expected = {normalise(n) for n in expected}
    folded_named = {normalise(n) for n in named}

    precision = len(folded_named & folded_seen) / len(folded_named) if folded_named else 1.0
    recall = len(folded_named & folded_expected) / len(folded_expected) if folded_expected else 1.0
    f1 = 2 * precision * recall / (precision + recall) if precision + recall else 0.0
    return precision, recall, f1


# What an answer says when a search returned nothing. A scalar of 0 is reported in
# prose far more often than as the digit -- "there's no climbing data near those
# coordinates" is the correct answer, and demanding "0" would fail it.
EMPTY_PHRASES = ("no climbing", "nothing", "no results", "no areas", "no crags", "no routes", "none")


def reports_empty(text: str) -> bool:
    """Whether the answer says a search came back empty."""
    return "0" in text or any(mentions(text, p) for p in EMPTY_PHRASES)
