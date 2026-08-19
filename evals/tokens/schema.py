from __future__ import annotations

import asyncio
import json
import logging

from mcp.types import Tool

from common.client import mcp_session
from common.tokenizer import count

logger = logging.getLogger(__name__)


def schema_size(tool: Tool) -> int:
    """Size of one tool's definition."""
    definition = {
        "name": tool.name,
        "description": tool.description or "",
        "input_schema": tool.input_schema,
    }
    return count(json.dumps(definition, separators=(",", ":")))


async def report() -> None:
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


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    asyncio.run(report())


if __name__ == "__main__":
    main()
