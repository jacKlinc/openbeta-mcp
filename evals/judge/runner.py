"""Run the golden set against a model and record what happened. See judge/README.md."""

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
from common.mlflow_export import export
from judge.dataset import GoldenSet
from judge.export import EXPERIMENT, RUN_KEY, log_run
from judge.models import Case
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
    """Tokens across every model call of one case."""

    model_config = ConfigDict(extra="forbid")

    input_tokens: int = 0
    output_tokens: int = 0
    cache_read_input_tokens: int = 0
    cache_creation_input_tokens: int = 0

    def add(self, usage: Any) -> None:
        """Accumulate one response's usage.

        The cache fields are recorded to prove caching stayed off, and are read
        defensively because the SDK does not guarantee them on every response.
        """
        self.input_tokens += usage.input_tokens
        self.output_tokens += usage.output_tokens
        self.cache_read_input_tokens += getattr(usage, "cache_read_input_tokens", 0) or 0
        self.cache_creation_input_tokens += getattr(usage, "cache_creation_input_tokens", 0) or 0


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


class AgentRunner:
    """Drives one sweep: a model, the tools it may call, and the rows produced.

    Args:
        client: anything exposing `messages.create`; the tests pass a stub.
        session: None when tools are disabled, which is the baseline run.
        tools: schemas offered to the model, empty when session is None.
        run: the run id shared by every row of this sweep.
    """

    def __init__(
        self,
        client: CreatesMessages,
        session: ClientSession | None,
        tools: list[dict[str, Any]],
        run: str,
    ) -> None:
        self.client = client
        self.session = session
        self.tools = tools
        self.run = run
        self.settings = get_settings()

    async def ask(self, messages: list[dict[str, Any]]) -> Message:
        """One model call."""
        return await self.client.messages.create(
            model=self.settings.model,
            max_tokens=self.settings.max_tokens,
            system=SYSTEM,
            messages=messages,
            # No temperature: removed from the Messages API in SDK 1.x, so variance
            # is measured across --runs rather than damped.
            # No cache_control: caching on would measure cache behaviour, not payload size.
            **({"tools": self.tools} if self.tools else {}),
        )

    async def execute(self, uses: list[ToolUseBlock], turn: int) -> tuple[list[ToolCall], list[dict[str, Any]]]:
        """Run the requested tools.

        Returns:
            The records to keep, and the tool_result blocks to send back. Both come
            from the same call, which is why they are built together. A tool error
            is returned to the model rather than raised: recovering from one is
            behaviour worth grading.
        """
        calls: list[ToolCall] = []
        results: list[dict[str, Any]] = []

        for use in uses:
            if self.session is None:  # --no-tools bound none, so this cannot happen
                raise RuntimeError("model requested a tool with tools disabled")
            called = await self.session.call_tool(use.name, use.input)
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
            results.append(
                {
                    "type": "tool_result",
                    "tool_use_id": use.id,
                    "content": text,
                    "is_error": bool(called.is_error),
                }
            )

        return calls, results

    async def run_case(self, case: Case, attempt: int) -> Result:
        """One case, one attempt.

        Returns:
            A row either way: an API or tool failure is recorded in `error` rather
            than raised, so one bad case cannot end a sweep that costs money.
        """
        started = time.monotonic()
        messages: list[dict[str, Any]] = [{"role": "user", "content": case.user_input}]
        calls: list[ToolCall] = []
        usage = Usage()
        stop_reason: str | None = None
        error: str | None = None
        answer = ""
        turn = 0

        try:
            while turn < self.settings.max_turns:
                turn += 1
                response = await self.ask(messages)
                usage.add(response.usage)
                stop_reason = response.stop_reason

                # Keep the last non-empty text: a tool_use turn often carries
                # preamble that would otherwise overwrite the real answer.
                answer = "".join(b.text for b in response.content if isinstance(b, TextBlock)) or answer

                if response.stop_reason != "tool_use":
                    break

                messages.append({"role": "assistant", "content": response.content})
                turn_calls, results = await self.execute(
                    [b for b in response.content if isinstance(b, ToolUseBlock)], turn
                )
                calls.extend(turn_calls)
                messages.append({"role": "user", "content": results})
            else:
                error = f"hit max_turns={self.settings.max_turns} without finishing"
        except (AnthropicError, MCPError, OSError, json.JSONDecodeError) as exc:
            error = f"{type(exc).__name__}: {exc}"
            logger.warning("%s: %s", case.case_id, error)

        return self._result(
            case,
            attempt,
            answer=answer,
            calls=calls,
            turns=turn,
            stop_reason=stop_reason,
            usage=usage,
            ms=int((time.monotonic() - started) * 1000),
            error=error,
        )

    def _result(self, case: Case, attempt: int, **outcome: Any) -> Result:
        """One row: what this sweep is, plus what this case did."""
        return Result(
            run_id=self.run,
            case_id=case.case_id,
            capability=case.capability,
            category=case.category,
            model=self.settings.model,
            tools_enabled=bool(self.tools),
            attempt=attempt,
            harness_version=harness_version(),
            tool_server_sha=tool_server_sha(),
            endpoint=self.settings.openbeta_endpoint,
            max_crags=self.settings.openbeta_max_crags,
            answer=outcome["answer"],
            tool_calls=outcome["calls"],
            turns=outcome["turns"],
            stop_reason=outcome["stop_reason"],
            usage=outcome["usage"],
            ms=outcome["ms"],
            error=outcome["error"],
        )

    async def sweep(self, cases: list[Case], runs: int, out: Path) -> list[Result]:
        """Run every case, appending a row per attempt.

        Args:
            runs: attempts per case. Repeats are the only way to measure variance,
                since temperature was removed from the Messages API.
            out: JSONL to append to; the source of truth, written before any export.
        """
        rows: list[Result] = []
        logger.info(
            "run %s: %d cases x %d attempts, model=%s tools=%s",
            self.run,
            len(cases),
            runs,
            self.settings.model,
            bool(self.tools),
        )

        for attempt in range(1, runs + 1):
            for case in cases:
                row = await self.run_case(case, attempt)
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


def build_client() -> AsyncAnthropic:
    """The Anthropic client, failing early if there are no credentials."""
    settings = get_settings()
    if not settings.anthropic_api_key:
        raise RuntimeError(
            "no ANTHROPIC_API_KEY in the environment or evals/.env -- every case in this sweep costs money, "
            "so it fails here rather than partway through"
        )
    return AsyncAnthropic(
        api_key=settings.anthropic_api_key,
        # An identity-linked key must name a workspace; harmless on a scoped key.
        default_headers=(
            {"anthropic-workspace-id": settings.anthropic_workspace_id} if settings.anthropic_workspace_id else {}
        ),
    )


async def sweep(cases: list[Case], *, use_tools: bool, runs: int, out: Path) -> list[Result]:
    """Build a runner against the real server and run the set through it."""
    async with mcp_session(env=get_settings().server_env()) as session:
        tools = await tool_schemas(session) if use_tools else []
        runner = AgentRunner(build_client(), session if use_tools else None, tools, run_id())
        return await runner.sweep(cases, runs, out)


def summarise(rows: list[Result]) -> None:
    """Log the totals, and any case that answered without calling a tool."""
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
    """Run the set, write the rows, and push them to MLflow."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--no-tools", action="store_true", help="baseline: same questions, no tools bound")
    parser.add_argument("--runs", type=int, default=1, help="attempts per case, to measure variance")
    args = parser.parse_args()

    cases = GoldenSet().cases()
    rows = asyncio.run(sweep(cases, use_tools=not args.no_tools, runs=args.runs, out=DEFAULT_OUT))
    summarise(rows)
    logger.info("wrote %s", DEFAULT_OUT)

    # The JSONL is written and is the source of truth; MLflow is a view
    try:
        export([DEFAULT_OUT], EXPERIMENT, force=False, key=RUN_KEY, log_run=log_run)
    except (MlflowException, OSError) as exc:
        logger.warning("mlflow export failed (%s); rows are still on disk", exc)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
