"""Environment the harness reads, and provenance for what it ran against.

One declaration for both, because the cost sweep and the judge runs have to agree
on which server they measured. They were previously four os.environ lookups with
three different fallbacks -- "public", "default", and the real default -- so two
datasets from the same afternoon could disagree about the endpoint they came from.
"""

from __future__ import annotations

import logging
import subprocess
import tomllib
from functools import cache
from pathlib import Path

from pydantic import field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

logger = logging.getLogger(__name__)

EVALS_ROOT = Path(__file__).parents[1]
REPO_ROOT = EVALS_ROOT.parent


class ProvenanceError(RuntimeError):
    """A run cannot be attributed to a version of the code or data."""


class Settings(BaseSettings):
    """The server under test, and where to find the data it serves."""

    model_config = SettingsConfigDict(
        env_prefix="",
        extra="ignore",
        # The repo .env is what scripts/ and the server itself read. Loading it
        # here means a recorded endpoint is the one actually in use rather than
        # the default. A real environment variable still wins over the file.
        env_file=REPO_ROOT / ".env",
    )

    # Local by default, unlike the server's own default of api.openbeta.io. The
    # golden set's expectations are pinned to the seeded local snapshot, so
    # falling back to live would regenerate them against different data and
    # record a fingerprint that then fails every subsequent --check.
    openbeta_endpoint: str = "http://localhost:4000/"
    # Matches MaxCrags in internal/tools/crags_near.go. Set explicitly rather than
    # left unset, so a run records the cap that was actually in force -- cost
    # against quality per cap is meant to be a join, and a null does not join.
    openbeta_max_crags: int = 20
    graphql_dir: Path = Path.home() / "repos/openbeta/openbeta-graphql"

    # Haiku for the first pass: the sweep is 27 cases x variants x repeats, and
    # design.md wants two model sizes anyway -- "trimming hurts the small model
    # more" is a finding, and the cheap axis is the one to iterate on.
    # Declared so the key can live in .env alongside everything else. The SDK
    # reads os.environ, not .env, so without this a key in the file is invisible
    # to the client and the run fails as if there were no credentials at all.
    anthropic_api_key: str | None = None
    # Required when the key is identity-linked: such a key is not bound to one
    # workspace, so the request has to name which workspace it bills to.
    anthropic_workspace_id: str | None = None

    model: str = "claude-haiku-4-5"
    # Anything else is a placeholder. design.md wants the judge to be a different
    # model from the one under test, so this is not merely a cost setting.
    judge_model: str = "claude-sonnet-5"
    max_tokens: int = 4096
    # A 2-step chain is the deepest case in the set; 6 leaves room for a recovery
    # without letting a confused model spin up a bill.
    max_turns: int = 6

    @field_validator("openbeta_endpoint")
    @classmethod
    def endpoint_has_a_scheme(cls, v: str) -> str:
        """A bare host:port reaches the GraphQL client as a relative URL.

        It fails deep inside the transport with an error naming neither the
        setting nor the value, so catch it here where the message can say both.
        """
        if not v.startswith(("http://", "https://")):
            raise ValueError(f"{v!r} needs a scheme, e.g. http://{v}")
        return v

    def server_env(self) -> dict[str, str]:
        """What to hand mcp_session, so the subprocess matches what gets recorded.

        Callers used to assemble this dict themselves, which is how a run ends up
        reporting an endpoint it never called. One mapping, one place.
        """
        return {
            "OPENBETA_ENDPOINT": self.openbeta_endpoint,
            "OPENBETA_MAX_CRAGS": str(self.openbeta_max_crags),
        }


@cache
def get_settings() -> Settings:
    """Settings, resolved on first use.

    Deliberately not a module-level instance: a bad OPENBETA_ENDPOINT in .env
    would then fail at import, taking down offline work that never touches the
    network. Resolved lazily, the error appears when the endpoint is needed.
    """
    return Settings()


def git_sha(repo: Path) -> str:
    """HEAD of a git checkout.

    Raises rather than returning None: an unrecorded sha means a result is pinned
    to nothing, and a run that cannot be attributed cannot be compared with a
    later one, which is the only reason to record provenance at all.
    """
    if not (repo / ".git").exists():
        raise ProvenanceError(f"no git checkout at {repo}, so provenance cannot be recorded")
    try:
        out = subprocess.run(
            ["git", "-C", str(repo), "rev-parse", "HEAD"],
            capture_output=True,
            text=True,
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        raise ProvenanceError(f"git rev-parse failed in {repo}: {(exc.stderr or '').strip()}") from exc
    except OSError as exc:
        raise ProvenanceError(f"could not run git in {repo}: {exc}") from exc

    sha = out.stdout.strip()
    if not sha:
        raise ProvenanceError(f"git rev-parse returned nothing in {repo}")
    return sha


def tool_server_sha() -> str:
    """Commit of the Go server under test. Same repo as this harness."""
    return git_sha(REPO_ROOT)


def graphql_sha() -> str:
    """Commit of the openbeta-graphql checkout serving the local snapshot."""
    return git_sha(get_settings().graphql_dir)


def harness_version() -> str:
    """Declared version of this harness, from pyproject.toml.

    Read from the file rather than importlib.metadata because evals is a uv
    virtual project with no build backend, so nothing installs it and the
    metadata does not exist. Complements tool_server_sha rather than duplicating
    it: the sha pins the exact code, this records the version someone bumped.
    """
    pyproject = EVALS_ROOT / "pyproject.toml"
    try:
        return tomllib.loads(pyproject.read_text())["project"]["version"]
    except (OSError, tomllib.TOMLDecodeError, KeyError) as exc:
        raise ProvenanceError(f"could not read a version from {pyproject}: {exc}") from exc
