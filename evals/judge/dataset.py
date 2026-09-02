"""Reading and writing the golden set: the cases and the manifest they are pinned to."""

from __future__ import annotations

from datetime import UTC, datetime
from pathlib import Path

from common.config import get_settings, graphql_sha, harness_version, tool_server_sha
from judge.models import Case, Manifest
from judge.payload import GroundTruthError

DATA = Path(__file__).parent / "data"

# Run outputs, beside the cost datasets rather than in the package.
RUNS = Path(__file__).parents[2] / "data" / "judge" / "runs.jsonl"
GRADES = RUNS.parent / "grades.jsonl"


class GoldenSet:
    """The cases and their manifest, read and written together.

    Args:
        data: directory holding golden-set.jsonl and manifest.json. Defaults to
            the committed set; a test can point at a fixture instead.
    """

    def __init__(self, data: Path = DATA) -> None:
        self.cases_path = data / "golden-set.jsonl"
        self.manifest_path = data / "manifest.json"

    def cases(self) -> list[Case]:
        """Cases in file order.

        Raises:
            GroundTruthError: a line that will not parse, named by line number, or
                a duplicate case_id.
        """
        cases: list[Case] = []
        for lineno, line in enumerate(self.cases_path.read_text().splitlines(), start=1):
            line = line.strip()
            if not line:
                continue
            try:
                cases.append(Case.model_validate_json(line))
            except ValueError as exc:
                raise GroundTruthError(f"{self.cases_path}:{lineno}: {exc}") from exc

        ids = [c.case_id for c in cases]
        if len(set(ids)) != len(ids):
            dupes = sorted({i for i in ids if ids.count(i) > 1})
            raise GroundTruthError(f"{self.cases_path}: duplicate case_id {dupes}")
        return cases

    def manifest(self) -> Manifest:
        """What the expected values are currently pinned to."""
        return Manifest.model_validate_json(self.manifest_path.read_text())

    def write(self, cases: list[Case], fingerprints: dict[str, str]) -> None:
        """Write the cases back and rebuild the manifest.

        The manifest is rebuilt rather than updated: every field is written on every
        run, which is what lets the model require them all.

        Args:
            cases: the full set, with generated values filled in.
            fingerprints: per case_id, the hash of the uuids its query returned.
        """
        settings = get_settings()
        with self.cases_path.open("w") as handle:
            for case in cases:
                handle.write(case.model_dump_json(exclude_none=True) + "\n")

        manifest = Manifest(
            harness_version=harness_version(),
            tool_server_sha=tool_server_sha(),
            openbeta_graphql_sha=graphql_sha(),
            snapshot_date=datetime.now(UTC).date().isoformat(),
            endpoint=settings.openbeta_endpoint,
            max_crags=settings.openbeta_max_crags,
            generated_at=datetime.now(UTC).isoformat(),
            fingerprint=fingerprints,
        )
        self.manifest_path.write_text(manifest.model_dump_json(indent=2) + "\n")
