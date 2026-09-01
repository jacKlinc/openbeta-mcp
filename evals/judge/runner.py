"""Run the golden set against a model talking to the real MCP server.

Records what happened; grades nothing. Scoring is a separate pass, and it needs
the exact tool output the model had in context -- the judge checks entailment
against that, not against the world -- so capture is this module's whole job.

A manual loop rather than client.beta.messages.tool_runner: the runner does not
expose per-call usage, and usage is half of what the study measures.

    python -m judge.runner                     # all cases, tools on
    python -m judge.runner --no-tools          # the baseline the server must beat
    python -m judge.runner --cases sport_first_lead_rumney --runs 3
"""

from __future__ import annotations

import argparse
import asyncio
import json
import logging
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Protocol

from anthropic import AnthropicError, AsyncAnthropic
from anthropic.types import Message, TextBlock, ToolUseBlock
from mcp import ClientSession, MCPError
from mlflow.exceptions import MlflowException
from pydantic import BaseModel, ConfigDict, Field

from common import jsonl
from common.client import mcp_session, text_of
from common.config import get_settings, harness_version, tool_server_sha
from common.mlflow_export import TRACKING_URI, export
from judge.export import EXPERIMENT, RUN_KEY, log_run
from judge.groundtruth import GOLDEN_SET, Case, load_cases
from tokens.corpus import args_sha

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

REPO_ROOT = Path(__file__).parents[2]
DEFAULT_OUT = REPO_ROOT / "data" / "judge" / "runs.jsonl"

ROW_VERSION = 1

SYSTEM = """You are helping a climber find routes, using the OpenBeta tools available to you.

Answer only from what the tools return. If the data does not cover something the \
climber asked about, say so plainly rather than filling the gap -- an honest \
"the data does not record that" is worth more than a plausible guess.

Grades and safety notes in OpenBeta are user-contributed opinions, not facts. Say \
so when relaying anything safety-critical, and suggest checking a current local \
guidebook."""

# TODO: pydantic.Field.description for all models


class ToolCall(BaseModel):
    """One tool call and what came back.

    The result text is kept verbatim, because it is the context the judge grades
    the final answer against.
    """

    model_config = ConfigDict(extra="forbid")

    turn: int
    name: str
    args: dict[str, Any]
    args_sha: str
    result: str
    is_error: bool


class Usage(BaseModel):
    model_config = ConfigDict(extra="forbid")

    input_tokens: int = 0
    output_tokens: int = 0
    cache_read_input_tokens: int = 0
    cache_creation_input_tokens: int = 0


class Result(BaseModel):
    """One (run_id, case_id) row. Graders fill in the scores later."""

    model_config = ConfigDict(extra="forbid")

    v: int = ROW_VERSION
    run_id: str
    case_id: str
    capability: str
    category: str  # TODO: enum

    model: str
    provider: str = "anthropic"
    tools_enabled: bool
    attempt: int

    harness_version: str
    tool_server_sha: str
    endpoint: str
    max_crags: int

    answer: str
    tool_calls: list[ToolCall] = Field(default_factory=list)
    turns: int
    stop_reason: str | None
    usage: Usage
    ms: int
    error: str | None = None

    ts: str = Field(default_factory=lambda: datetime.now(UTC).isoformat())


def run_id() -> str:
    """Identifier shared by every row of one sweep, matching sweep.py's shape."""
    return f"judge-{datetime.now(UTC):%Y%m%dT%H%M%SZ}"


async def tool_schemas(session: ClientSession) -> list[dict[str, Any]]:
    """MCP tool definitions as Anthropic tool params.

    Both sides speak JSON Schema, so inputSchema crosses unchanged. Taken from
    the live server rather than hand-written, so a schema edit in Go reaches the
    model without anyone remembering to mirror it here.
    """
    listed = await session.list_tools()
    return [{"name": t.name, "description": t.description or "", "input_schema": t.input_schema} for t in listed.tools]


class Messages(Protocol):
    async def create(self, **kwargs: Any) -> Message: ...


class CreatesMessages(Protocol):
    """What run_case needs of a client: the real one and the test stub both fit."""

    @property
    def messages(self) -> Messages: ...


async def run_case(
    client: CreatesMessages,
    session: ClientSession | None,
    case: Case,
    tools: list[dict[str, Any]],
    run: str,
    attempt: int,
) -> Result:
    """One case, one attempt. Returns a row whether it succeeded or failed."""
    settings = get_settings()
    started = time.monotonic()
    messages: list[dict[str, Any]] = [{"role": "user", "content": case.user_input}]
    calls: list[ToolCall] = []
    usage = Usage()
    stop_reason: str | None = None
    error: str | None = None
    answer = ""
    turn = 0

    try:
        while turn < settings.max_turns:
            turn += 1
            # temperature was removed from the Messages API in SDK 1.x, so variance
            # must be measured across --runs rather than damped.
            response = await client.messages.create(
                model=settings.model,
                max_tokens=settings.max_tokens,
                system=SYSTEM,
                messages=messages,
                # No cache_control: caching on would measure cache behaviour, not payload size.
                **({"tools": tools} if tools else {}),
            )

            usage.input_tokens += response.usage.input_tokens
            usage.output_tokens += response.usage.output_tokens
            usage.cache_read_input_tokens += getattr(response.usage, "cache_read_input_tokens", 0) or 0
            usage.cache_creation_input_tokens += getattr(response.usage, "cache_creation_input_tokens", 0) or 0
            stop_reason = response.stop_reason

            answer = "".join(b.text for b in response.content if isinstance(b, TextBlock)) or answer

            if response.stop_reason != "tool_use":
                break

            uses = [b for b in response.content if isinstance(b, ToolUseBlock)]
            messages.append({"role": "assistant", "content": response.content})

            results = []
            for use in uses:
                if session is None:  # --no-tools bound none, so this cannot happen
                    raise RuntimeError("model requested a tool with tools disabled")
                called = await session.call_tool(use.name, use.input)
                text = text_of(called)
                calls.append(
                    ToolCall(
                        turn=turn,
                        name=use.name,
                        args=dict(use.input),
                        args_sha=args_sha(dict(use.input)),
                        result=text,
                        is_error=bool(called.is_error),
                    )
                )
                # Returned to the model, not raised: recovery is behaviour worth grading.
                results.append(
                    {
                        "type": "tool_result",
                        "tool_use_id": use.id,
                        "content": text,
                        "is_error": bool(called.is_error),
                    }
                )
            messages.append({"role": "user", "content": results})
        else:
            error = f"hit max_turns={settings.max_turns} without finishing"
    except (AnthropicError, MCPError, OSError, json.JSONDecodeError) as exc:
        error = f"{type(exc).__name__}: {exc}"
        logger.warning("%s: %s", case.case_id, error)

    return Result(
        run_id=run,
        case_id=case.case_id,
        capability=case.capability,
        category=case.category,
        model=settings.model,
        tools_enabled=bool(tools),
        attempt=attempt,
        harness_version=harness_version(),
        tool_server_sha=tool_server_sha(),
        endpoint=settings.openbeta_endpoint,
        max_crags=settings.openbeta_max_crags,
        answer=answer,
        tool_calls=calls,
        turns=turn,
        stop_reason=stop_reason,
        usage=usage,
        ms=int((time.monotonic() - started) * 1000),
        error=error,
    )


async def sweep(cases: list[Case], *, use_tools: bool, runs: int, out: Path) -> list[Result]:
    settings = get_settings()
    if not settings.anthropic_api_key:
        raise RuntimeError(
            "no ANTHROPIC_API_KEY in the environment or evals/.env -- every case in this sweep costs money, "
            "so it fails here rather than partway through"
        )
    client = AsyncAnthropic(
        api_key=settings.anthropic_api_key,
        # An identity-linked key must name a workspace; harmless on a scoped key.
        default_headers=(
            {"anthropic-workspace-id": settings.anthropic_workspace_id} if settings.anthropic_workspace_id else {}
        ),
    )
    run = run_id()
    rows: list[Result] = []

    logger.info(
        "run %s: %d cases x %d attempts, model=%s tools=%s",
        run,
        len(cases),
        runs,
        settings.model,
        use_tools,
    )

    async with mcp_session(env=settings.server_env()) as session:
        tools = await tool_schemas(session) if use_tools else []
        for attempt in range(1, runs + 1):
            for case in cases:
                row = await run_case(client, session if use_tools else None, case, tools, run, attempt)
                jsonl.append(out, row.model_dump())
                rows.append(row)
                logger.info(
                    "%s a%d: %d calls, %d turns, %d tok%s",
                    case.case_id,
                    attempt,
                    len(row.tool_calls),
                    row.turns,
                    row.usage.input_tokens + row.usage.output_tokens,
                    f", ERROR {row.error}" if row.error else "",
                )

    return rows


def summarise(rows: list[Result]) -> None:
    ok = [r for r in rows if not r.error]
    tokens = sum(r.usage.input_tokens + r.usage.output_tokens for r in rows)
    calls = sum(len(r.tool_calls) for r in rows)
    logger.info(
        "%d rows, %d failed, %d tool calls, %d tokens",
        len(rows),
        len(rows) - len(ok),
        calls,
        tokens,
    )
    silent = [r.case_id for r in ok if r.tools_enabled and not r.tool_calls]
    if silent:
        logger.info("answered without calling a tool: %s", ", ".join(sorted(set(silent))))


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--no-tools", action="store_true", help="baseline: same questions, no tools bound")
    parser.add_argument("--cases", nargs="*", help="case_ids to run; default all")  # TODO: remove
    parser.add_argument("--runs", type=int, default=1, help="attempts per case")  # TODO: remove
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)  # TODO: remove
    args = parser.parse_args()

    cases = load_cases(GOLDEN_SET)
    if args.cases:
        wanted = set(args.cases)
        missing = wanted - {c.case_id for c in cases}
        if missing:
            parser.error(f"unknown case_ids: {sorted(missing)}")
        cases = [c for c in cases if c.case_id in wanted]

    rows = asyncio.run(sweep(cases, use_tools=not args.no_tools, runs=args.runs, out=args.out))
    summarise(rows)
    logger.info("wrote %s", args.out)

    # The JSONL is written and is the source of truth; MLflow is a view
    try:
        export([args.out], EXPERIMENT, TRACKING_URI, force=False, key=RUN_KEY, log_run=log_run)
    except (MlflowException, OSError) as exc:
        logger.warning("mlflow export failed (%s); rows are still on disk", exc)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
