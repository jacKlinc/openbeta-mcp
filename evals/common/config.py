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
        # A real environment variable still wins over the file.
        env_file=REPO_ROOT / ".env",
    )

    # Local, unlike the server's own default: the golden set is pinned to the seeded snapshot.
    openbeta_endpoint: str = "http://localhost:4000/"
    openbeta_max_crags: int = 20  # matches MaxCrags in crags_near.go
    graphql_dir: Path = Path.home() / "repos/openbeta/openbeta-graphql"

    # The SDK reads os.environ, not .env, so a key in the file is invisible without this.
    anthropic_api_key: str | None = None
    # Required for an identity-linked key, which is not bound to one workspace.
    anthropic_workspace_id: str | None = None

    model: str = "claude-haiku-4-5"
    # design.md wants the judge to differ from the model under test.
    judge_model: str = "claude-sonnet-5"
    max_tokens: int = 4096
    # Deepest case is a 2-step chain; 6 allows a recovery without a runaway bill.
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
