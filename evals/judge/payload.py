"""Reading a tool response: the expected value, and what fingerprints it."""

from __future__ import annotations

import hashlib
from typing import Any

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
