"""The agent loop, driven by a stub model.

Every real model call costs money, so the loop is tested against a scripted
client. The MCP server is real, so the tool output is what a model would see.

TODO: the golden set's own validation (USA-only places, tool parameters that
exist, prose cases carrying a reason) is enforced by the pydantic models in
judge/groundtruth.py but has no tests of its own.

TODO: no test covers a model calling two tools in one turn. The loop handles it,
but nothing proves it.
"""

from __future__ import annotations

import os
from types import SimpleNamespace

import httpx
import pytest
from anthropic import InternalServerError
from anthropic.types import TextBlock, ToolUseBlock

from common.client import mcp_session
from common.config import get_settings
from judge.dataset import GoldenSet
from judge.models import TOOL_ARGS
from judge.runner import run_case, tool_schemas

CASE_ID = "sport_first_lead_rumney"


# --- stub model -------------------------------------------------------------
# A response is a SimpleNamespace, but its content blocks are real SDK types:
# the loop narrows with isinstance, so a look-alike would be silently ignored.


def usage():
    return SimpleNamespace(
        input_tokens=100,
        output_tokens=20,
        cache_read_input_tokens=0,
        cache_creation_input_tokens=0,
    )


def says(text):
    """A final answer: no tool call, loop ends."""
    return SimpleNamespace(
        stop_reason="end_turn",
        usage=usage(),
        content=[TextBlock(type="text", text=text)],
    )


def calls(tool, **args):
    """A tool call: loop runs the tool and goes round again."""
    return SimpleNamespace(
        stop_reason="tool_use",
        usage=usage(),
        content=[ToolUseBlock(type="tool_use", id="tu_1", name=tool, input=args)],
    )


class StubModel:
    """Replays scripted responses. The last one repeats if the loop keeps going."""

    def __init__(self, *responses):
        self.responses = responses
        self.n = 0
        self.messages = self  # so runner can call client.messages.create(...)

    async def create(self, **kwargs):
        response = self.responses[min(self.n, len(self.responses) - 1)]
        self.n += 1
        if isinstance(response, Exception):
            raise response
        return response


async def run(model, case_id=CASE_ID, with_tools=True):
    """Run one case against a stub model and the real MCP server."""
    case = next(c for c in GoldenSet().cases() if c.case_id == case_id)
    async with mcp_session(env=get_settings().server_env()) as session:
        tools = await tool_schemas(session) if with_tools else []
        return await run_case(model, session if with_tools else None, case, tools, "test", 1)


# --- tests ------------------------------------------------------------------


async def test_tools_offered_to_the_model_are_the_real_ones():
    """If these drift apart, every case grades a tool call that cannot work."""
    async with mcp_session(env=get_settings().server_env()) as session:
        tools = await tool_schemas(session)

    assert {t["name"] for t in tools} == {"crags_near", "find_climbs", "get_area_details"}
    for tool in tools:
        assert set(tool["input_schema"]["properties"]) == TOOL_ARGS[tool["name"]]


async def test_calls_a_tool_then_answers():
    row = await run(
        StubModel(
            calls("find_climbs", place="Rumney", disciplines=["sport"]),
            says("Here are some routes."),
        )
    )

    assert row.error is None
    assert row.turns == 2
    assert row.answer == "Here are some routes."
    assert [c.name for c in row.tool_calls] == ["find_climbs"]


@pytest.mark.skipif(
    not os.environ.get("OPENBETA_SEEDED"),
    reason="needs a seeded graphql stack; set OPENBETA_SEEDED=1 after scripts/dev-up.sh",
)
async def test_tool_output_is_kept_verbatim():
    """The judge grades the answer against this text, so it must be the real thing.

    The only test here that reads real climbing data. The rest need the binary
    but not the database, because a tool error is valid input to the loop.
    """
    row = await run(StubModel(calls("find_climbs", place="Rumney"), says("done")))

    result = row.tool_calls[0].result
    assert '"climbs"' in result
    assert "Rumney" in result


async def test_usage_adds_up_across_turns():
    """Two model calls at 100 in / 20 out each."""
    row = await run(StubModel(calls("find_climbs", place="Rumney"), says("done")))

    assert row.usage.input_tokens == 200
    assert row.usage.output_tokens == 40


async def test_a_failed_api_call_becomes_a_row():
    """One bad case must not end a sweep that costs money to restart."""
    overloaded = InternalServerError(
        "overloaded", response=httpx.Response(529, request=httpx.Request("POST", "/")), body=None
    )
    row = await run(StubModel(overloaded))

    assert "InternalServerError" in row.error
    assert row.answer == ""


async def test_a_harness_bug_is_not_recorded_as_a_failed_case():
    """A TypeError is our mistake, not the model's; recording it hides it.

    It surfaces wrapped in an ExceptionGroup, because the MCP session runs the
    loop inside an anyio task group.
    """
    with pytest.raises(BaseException) as caught:
        await run(StubModel(TypeError("unexpected keyword argument")))

    assert "TypeError" in repr(caught.value)


async def test_a_model_that_never_stops_is_capped():
    row = await run(StubModel(calls("find_climbs", place="Rumney")))

    assert row.turns == get_settings().max_turns
    assert "max_turns" in row.error


async def test_a_tool_error_goes_back_to_the_model():
    """Recovering from a tool error is behaviour to grade, not a harness failure."""
    row = await run(
        StubModel(
            calls("crags_near", place="Nowhere-at-all"),
            says("I could not find that place."),
        )
    )

    assert row.error is None
    assert row.tool_calls[0].is_error is True
    assert row.answer == "I could not find that place."


async def test_no_tools_mode_offers_none():
    """The baseline run: same question, no tools, so the answer is priors only."""
    row = await run(StubModel(says("Where are you?")), with_tools=False)

    assert row.tools_enabled is False
    assert row.tool_calls == []
