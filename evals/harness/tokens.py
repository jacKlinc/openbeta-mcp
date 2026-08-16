import asyncio

from client import mcp_session
from mcp.types import CallToolResult, TextContent


def text_of(result: CallToolResult) -> str:
    """Join the text blocks of a tool result"""
    return "".join(b.text for b in result.content if isinstance(b, TextContent))


async def main():
    async with mcp_session() as session:
        result = await session.call_tool(
            "get_area_details", {"areaId": "8f267065-fc1a-59ce-bcf1-6e9335548363"}
        )

        if result.is_error:
            raise RuntimeError(f"get_area_details failed: {text_of(result)}")

        print(text_of(result))


if __name__ == "__main__":
    asyncio.run(main())
