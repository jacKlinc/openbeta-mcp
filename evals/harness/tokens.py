import asyncio
from pathlib import Path

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client


async def run_client():
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
        print("Successfully initialized MCP Client!")

        # 4. Interact with the server's exposed tools
        tools_response = await session.list_tools()
        print("\nAvailable tools on the server:")
        for tool in tools_response.tools:
            print(f"- {tool.name}: {tool.description}")

        # 5. Example of invoking a specific tool (if 'fetch_data' exists)
        # result = await session.call_tool("fetch_data", arguments={"param": "value"})
        # print(f"Tool response: {result.content}")


if __name__ == "__main__":
    asyncio.run(run_client())
