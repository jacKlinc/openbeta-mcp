from __future__ import annotations

import tiktoken

# OpenAI's encoding, not Claude's. It undercounts Claude by roughly 15-20% on
# prose and by more on JSON, so every count derived from it is a relative
# measure — see the caveat in evals/README.md.
ENCODING = "o200k_base"

encoding = tiktoken.get_encoding(ENCODING)


def count(text: str) -> int:
    """Token count under ENCODING. Relative measure only."""
    return len(encoding.encode(text))
