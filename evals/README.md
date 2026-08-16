# evals

Harness for measuring what the openbeta MCP server costs a model.

```bash
cd harness
uv run python tokens.py
```

It connects to the built binary over stdio, lists the tools, and reports the
size of each tool's schema and of each tool's output.

## What the numbers are

`tiktoken`'s `o200k_base` is OpenAI's encoding, not Claude's, so **nothing here
is a Claude token count**. It undercounts Claude by roughly 15–20% on prose and
by more on JSON, which is exactly the shape of these tool results.

Treat every figure as a *relative* measure. Comparing two payloads, or watching
one grow across a change, is sound. Quoting one as "this call costs N tokens" is
not — and because the gap is wider on JSON than on prose, the schema-versus-output
balance shown here flatters the schemas.

## Why not real Claude counts

`anthropic.messages.count_tokens` is the authoritative count: the real model
tokenizer, server-side. It runs no inference and bills no tokens — but it still
goes through the developer API, and **a Claude Pro/Max subscription does not pay
for that**. On an unfunded account every call returns:

```
400 invalid_request_error: Your credit balance is too low to access the Anthropic API.
```

That path was built and removed. Bringing it back is `uv add anthropic` plus a
funded account; counts are model-specific, so pass the model the server is
actually evaluated against.

The free alternative is **`/context` in Claude Code**, which breaks the live
context window down by component — MCP tool schemas included — using the real
tokenizer, paid for by the subscription.

Claude Code transcripts under `~/.claude/projects/*/*.jsonl` also carry genuine
`usage` records (`cache_creation_input_tokens`, `cache_read_input_tokens`).
Deriving a tiktoken correction factor from consecutive-turn deltas was tried and
abandoned: across 600 pairs the implied ratio ranged from 1.63 to 2.39 chars per
token, because each delta also sweeps in system reminders and cache-boundary
effects. The `usage` numbers are real; that attribution is not.

## Two costs, billed differently

- **Tool output** is paid once, when the result comes back.
- **Tool schemas** are paid on every single turn, whether or not the tool is
  called. That standing rent is why adding a tool is never free, and it is the
  measurement behind the tool-shape decision in
  [../docs/gazzetteer/README.md](../docs/gazzetteer/README.md) — one folded tool
  versus two separate ones.
