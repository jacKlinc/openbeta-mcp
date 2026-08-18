from __future__ import annotations

import os
import subprocess
from pathlib import Path

RUN_ENV = "OPENBETA_RUN"

REPO_ROOT = Path(__file__).parents[2]


def _git(*args: str) -> str:
    out = subprocess.run(
        ["git", *args],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        check=True,
    )
    return out.stdout.strip()


def commit() -> str:
    """Full commit sha of the working tree."""
    return _git("rev-parse", "HEAD")


def dirty() -> bool:
    """Whether the tree has uncommitted changes.

    Untracked files count, matching Go's stamp: `git status --porcelain` has no
    -uno there, so a sample recorded beside a stray file is dirty either way.
    """
    return bool(_git("status", "--porcelain"))


def run_id(prefix: str = "tok") -> str:
    """Identifier shared by every row of one sweep.

    $OPENBETA_RUN wins when set, so the harness and the server's own sink agree
    on the run id for the same calls.
    """
    return os.environ.get(RUN_ENV) or f"{prefix}-{_git('rev-parse', '--short', 'HEAD')}"
