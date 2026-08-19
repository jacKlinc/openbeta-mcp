from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

import pandas as pd


def append(path: Path, record: dict[str, Any]) -> None:
    """Append one record as a single line."""
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a") as handle:
        handle.write(json.dumps(record) + "\n")


def read_df(path: Path) -> tuple[pd.DataFrame, int]:
    """Return the records as a frame, and the count of unparseable lines.

    The file is append-only and written by a long-lived server, so a truncated
    final line is plausible. A bad line is reported and skipped, never fatal —
    which is why this is a loop rather than pd.read_json(lines=True).
    """
    records: list[dict] = []
    malformed = 0

    with path.open() as handle:
        for lineno, line in enumerate(handle, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError as exc:
                malformed += 1
                print(f"{path}:{lineno}: skipping malformed line: {exc}", file=sys.stderr)

    return pd.DataFrame(records), malformed
