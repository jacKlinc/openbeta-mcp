from __future__ import annotations

import argparse
import asyncio
import json
import logging
import os
import time
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from mcp.types import CallToolResult

from common import jsonl
from common.client import mcp_session, text_of
from common.tokenizer import ENCODING, count
from tokens.corpus import args_sha, build

logger = logging.getLogger(__name__)

REPO_ROOT = Path(__file__).parents[2]
DEFAULT_OUT = REPO_ROOT / "data" / "tokens" / "data.jsonl"
DEFAULT_CACHE = REPO_ROOT / "data" / "tokens" / "cache"

# Row schema version, matching the Go sink's sampleVersion. One since the commit
# stamp came out; v0 rows carry commit and dirty fields that v1 does not.
ROW_VERSION = 1

# Pins the run id, so the harness and the server's own sink label the same calls
# as one experiment. The runner script sets it.
RUN_ENV = "OPENBETA_RUN"


def run_id() -> str:
    """Identifier shared by every row of one sweep.

    A UTC timestamp rather than a commit: it sorts, it needs no git call, and
    MLflow tags the export with the commit anyway.
    """
    return os.environ.get(RUN_ENV) or f"tok-{datetime.now(UTC):%Y%m%dT%H%M%SZ}"


def row(tool: str, args: dict[str, Any], text: str, err: bool, run: str) -> dict:
    """One dataset row for one call.

    Field names match the server's own sink (internal/mcpserver/metrics.go), so
    the two datasets read alike and join on (tool, args_sha).
    """
    return {
        "v": ROW_VERSION,
        "run": run,
        "ts": datetime.now(UTC).isoformat(),
        "tool": tool,
        "args": args,
        "args_sha": args_sha(args),
        "tokens": count(text) if not err else 0,
        "chars": len(text),
        "err": err,
        "encoding": ENCODING,
    }


async def sweep(
    tools: list[str],
    out: Path,
    cache: Path,
    use_cache: bool,
    limit: int | None,
    delay: float,
) -> int:
    """Run the corpus and append a row per call. Returns rows written.

    Payloads are cached by args_sha, so re-counting after a tokenizer or
    analysis change costs nothing upstream. The API is free and volunteer-run,
    so calls are serial and paced.
    """
    corpus = build()
    run = run_id()
    logger.info("run %s", run)

    cache.mkdir(parents=True, exist_ok=True)
    written = 0
    started = time.monotonic()

    async with mcp_session_or_none(use_cache, corpus, tools, limit, cache) as session:
        for tool in tools:
            arg_sets = corpus[tool][:limit] if limit else corpus[tool]
            for i, args in enumerate(arg_sets, start=1):
                sha = args_sha(args)
                cached = cache / f"{sha}.json"

                if use_cache and cached.exists():
                    blob = json.loads(cached.read_text())
                    text, err = blob["text"], blob["err"]
                else:
                    # One dead UUID or unresolvable place must not end a sweep
                    # of three hundred calls, so the failure becomes a row.
                    try:
                        result = await session.call_tool(tool, args)
                        text, err = text_of(result), bool(result.is_error)
                    except Exception as exc:
                        logger.warning("%s %s: %s", tool, sha, exc)
                        text, err = "", True
                    cached.write_text(json.dumps({"tool": tool, "args": args, "text": text, "err": err}))
                    await asyncio.sleep(delay)

                record = row(tool, args, text, err, run)
                jsonl.append(out, record)
                written += 1
                logger.info(
                    "%-17s %3d/%-3d %6d tokens  %s",
                    tool,
                    i,
                    len(arg_sets),
                    record["tokens"],
                    json.dumps(args, separators=(",", ":")),
                )

    logger.info("%d rows in %.0fs -> %s", written, time.monotonic() - started, out)
    return written


class _NoSession:
    """Stand-in for a session when every payload is already cached."""

    async def call_tool(self, tool: str, args: dict) -> CallToolResult:
        raise RuntimeError(f"{tool} {args}: not cached and --use-cache was given")


@asynccontextmanager
async def _no_session():
    yield _NoSession()


def mcp_session_or_none(use_cache: bool, corpus: dict, tools: list[str], limit: int | None, cache: Path):
    """Skip launching the server when the cache already covers the sweep.

    A cache-only re-count should not need a built binary, let alone a network.
    """
    if use_cache:
        wanted = [args for tool in tools for args in (corpus[tool][:limit] if limit else corpus[tool])]
        if all((cache / f"{args_sha(a)}.json").exists() for a in wanted):
            logger.info("cache covers all %d calls; not launching the server", len(wanted))
            return _no_session()

    return mcp_session()


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(message)s")
    parser = argparse.ArgumentParser(description="Measure the token cost of every tool result in the corpus.")
    parser.add_argument(
        "--tool",
        default="all",
        choices=["all", "crags_near", "find_climbs", "get_area_details"],
    )
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--cache", type=Path, default=DEFAULT_CACHE)
    parser.add_argument("--use-cache", action="store_true", help="reuse cached payloads where present")
    parser.add_argument("--limit", type=int, help="first N argument sets per tool")
    parser.add_argument("--delay", type=float, default=0.2, help="seconds between live calls")
    args = parser.parse_args()

    tools = list(build()) if args.tool == "all" else [args.tool]
    asyncio.run(sweep(tools, args.out, args.cache, args.use_cache, args.limit, args.delay))


if __name__ == "__main__":
    main()
