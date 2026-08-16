import asyncio
import json
import logging

import tiktoken
from client import mcp_session
from mcp.types import CallToolResult, TextContent

encoding = tiktoken.get_encoding("o200k_base")

logging.basicConfig(level=logging.INFO)

logger = logging.getLogger(__name__)


def text_of(result: CallToolResult) -> str:
    """Join the text blocks of a tool result"""
    return "".join(b.text for b in result.content if isinstance(b, TextContent))


async def count_tokens(session, tool: str, args: dict):
    input_text = json.dumps(args, separators=(",", ":"))
    logger.info("Input tokens: %s", len(encoding.encode(input_text)))

    # Call tool and raise error
    result = await session.call_tool(tool, args)
    if result.is_error:
        raise RuntimeError(f"get_area_details failed: {text_of(result)}")

    output_text = json.dumps(result.model_dump(), separators=(",", ":"))
    output_tokens = len(encoding.encode(output_text))
    logger.info("Output tokens: %s", output_tokens)

    return output_tokens


async def main():
    async with mcp_session() as session:
        tokens = await count_tokens(
            session,
            "get_area_details",
            {"areaId": "8f267065-fc1a-59ce-bcf1-6e9335548363"},
        )


if __name__ == "__main__":
    asyncio.run(main())
