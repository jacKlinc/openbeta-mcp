import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.types import CallToolResult, TextContent

logging.basicConfig(level=logging.INFO)

logger = logging.getLogger(__name__)

# The binary is built at the repository root and gitignored; scripts/tokens.sh
# builds it before invoking any harness.
DEFAULT_BINARY = Path(__file__).parents[2] / "openbeta-mcp"


def text_of(result: CallToolResult) -> str:
    """Join the text blocks of a tool result.

    Only the text blocks: this is what the model reads. The SDK also emits
    structuredContent, whose cost depends on the client and is measured
    separately if at all.
    """
    return "".join(b.text for b in result.content if isinstance(b, TextContent))


@asynccontextmanager
async def mcp_session(env: dict[str, str] | None = None, binary: Path | None = None):
    """An initialised MCP session against the compiled server over stdio.

    `env` is merged over the current environment rather than replacing it. The
    SDK substitutes a minimal safe environment when env is None, which strips
    OPENBETA_METRICS and OPENBETA_RUN and silently disables the server's own
    telemetry sink — so the environment is always passed explicitly.
    """
    binary_path = str(binary or DEFAULT_BINARY)
    server_params = StdioServerParameters(
        command=binary_path,
        env={**os.environ, **(env or {})},
    )

    async with (
        # Establish the stdio connection transport layer
        stdio_client(server_params) as (read_stream, write_stream),
        ClientSession(read_stream, write_stream) as session,
    ):
        await session.initialize()
        logger.info("MCP session initialised")

        tools = await session.list_tools()
        logger.info("Tools available: %s", [t.name for t in tools.tools])

        yield session
