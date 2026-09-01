# Integration tests

Tests that need something running: the compiled `openbeta-mcp` binary, and behind
it a local `openbeta-graphql` stack with a seeded database.

```
./scripts/dev-up.sh          # from the repo root, brings up mongo + graphql on :4000
go build -o openbeta-mcp ./cmd/openbeta-mcp
cd evals && uv run pytest integration/
```

These are kept out of `tests/` so that CI, which has neither a database nor the
binary, can run the unit suite and report coverage without provisioning either.
Nothing here calls a model — the agent loop is driven by a stub — so they cost
nothing to run beyond the stack itself.
