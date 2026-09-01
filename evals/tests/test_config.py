"""Settings and provenance. No server, no network, nothing mocked."""

from __future__ import annotations

from pathlib import Path

import pytest
from pydantic import ValidationError

from common.config import ProvenanceError, Settings, git_sha, harness_version, tool_server_sha


def settings(**over) -> Settings:
    """Built without reading .env, so a developer's own file cannot skew a test."""
    return Settings(_env_file=None, **over)


def test_endpoint_must_have_a_scheme():
    with pytest.raises(ValidationError, match="needs a scheme"):
        settings(openbeta_endpoint="127.0.0.1:4000")


def test_endpoint_defaults_to_local():
    """The golden set is pinned to the local snapshot; live would rot it."""
    assert settings().openbeta_endpoint.startswith("http://localhost")


def test_server_env_is_what_the_subprocess_gets():
    env = settings(openbeta_endpoint="http://example/", openbeta_max_crags=5).server_env()

    assert env == {"OPENBETA_ENDPOINT": "http://example/", "OPENBETA_MAX_CRAGS": "5"}


def test_max_crags_is_an_int():
    """It is recorded in the manifest and joined on; "20" and 20 do not join."""
    assert settings(openbeta_max_crags="30").openbeta_max_crags == 30


def test_git_sha_raises_outside_a_checkout():
    with pytest.raises(ProvenanceError, match="no git checkout"):
        git_sha(Path("/tmp"))


def test_provenance_reads_this_repo_and_package():
    assert len(tool_server_sha()) == 40
    assert harness_version()
