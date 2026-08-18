import logging
from contextlib import asynccontextmanager
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

logging.basicConfig(level=logging.INFO)

logger = logging.getLogger(__name__)


@asynccontextmanager
async def mcp_session():
    # 1. Define how to start the target MCP server via stdio
    binary_path = str(Path(__file__).parents[2] / "./openbeta-mcp")
    server_params = StdioServerParameters(command=f"{binary_path}")

    async with (
        # 2. Establish the stdio connection transport layer
        stdio_client(server_params) as (read_stream, write_stream),
        # 2. Establish the stdio connection transport layer
        ClientSession(read_stream, write_stream) as session,
    ):
        await session.initialize()
        logger.info("MCP session initialised")

        tools = await session.list_tools()
        logger.info("Tools available: %s", [t.name for t in tools.tools])

        yield session
