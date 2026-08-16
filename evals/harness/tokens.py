import asyncio
import json
import logging

import tiktoken
from client import mcp_session
from mcp.types import CallToolResult, TextContent, Tool

encoding = tiktoken.get_encoding("o200k_base")

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def text_of(result: CallToolResult) -> str:
    """Join the text blocks of a tool result"""
    return "".join(b.text for b in result.content if isinstance(b, TextContent))


def count(text: str) -> int:
    """Token count under OpenAI's encoding. Relative measure only."""
    return len(encoding.encode(text))


def schema_size(tool: Tool) -> int:
    """Size of one tool's definition — the part resent on every turn."""
    definition = {
        "name": tool.name,
        "description": tool.description or "",
        "input_schema": tool.input_schema,
    }
    return count(json.dumps(definition, separators=(",", ":")))


async def measure(session, tool: str, args: dict) -> int:
    """Call a tool and report the size of what it returns."""
    result = await session.call_tool(tool, args)
    if result.is_error:
        raise RuntimeError(f"{tool} failed: {text_of(result)}")

    output = text_of(result)
    tokens = count(output)
    logger.info(
        "%-17s %5s tokens  %6s chars  %s",
        tool,
        tokens,
        len(output),
        json.dumps(args, separators=(",", ":")),
    )
    return tokens


CALLS = [
    ("crags_near", {"place": "Squamish", "maxDistanceKm": 5}),
    (
        "find_climbs",
        {
            "place": "Squamish",
            "minGrade": "5.8",
            "maxGrade": "5.10b",
            "multipitchOnly": True,
        },
    ),
    ("get_area_details", {"areaId": "8f267065-fc1a-59ce-bcf1-6e9335548363"}),
]


async def main():
    """Measure the size of MCP tool schemas and outputs."""
    async with mcp_session() as session:
        listed = await session.list_tools()

        logger.info("Tool schemas:")
        for tool in listed.tools:
            logger.info("%-17s %5s tokens", tool.name, schema_size(tool))
        logger.info(
            "%-17s %5s tokens total across %s tools",
            "",
            sum(schema_size(t) for t in listed.tools),
            len(listed.tools),
        )

        logger.info("Tool output:")
        for tool, args in CALLS:
            await measure(session, tool, args)


if __name__ == "__main__":
    asyncio.run(main())
