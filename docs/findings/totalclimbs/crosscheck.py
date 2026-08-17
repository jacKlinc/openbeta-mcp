#!/usr/bin/env python3
"""Cross-check totalClimbs against the climbs an area actually holds.

The three .graphql files next to this script each make one point by hand. This
generalises them: it walks a subtree and reports, over every leaf it reaches,
how often totalClimbs agrees with len(climbs).

Stdlib only, so it runs anywhere python3 does:

    ./crosscheck.py --area "Squamish"
    ./crosscheck.py --area "Yosemite Valley" --endpoint http://localhost:4000/

The public API is a free service run by volunteers and returns intermittent
502s under load, so requests are retried with a backoff and the crawl is one
nested request rather than one per area.

Exit status is 1 when any leaf disagrees, so this doubles as a check you can
run again later to see whether upstream has fixed it.
"""

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

DEFAULT_ENDPOINT = "https://api.openbeta.io/graphql"

# Nesting depth is fixed because GraphQL has no recursive fragments. Too shallow
# and the crawl silently understates the problem — at depth 4 British Columbia
# reports 7105 hidden climbs, at depth 8 it reports 7659 — so anything cut off is
# counted and warned about rather than dropped. Eight reaches every leaf in the
# subtrees measured in README.md.
DEPTH = 8

FIELDS = "areaName totalClimbs metadata { leaf } climbs { uuid }"


def build_query(depth: int) -> str:
    body = FIELDS
    for _ in range(depth):
        body = f"{FIELDS} children {{ {body} }}"
    return (
        "query Deep($name: String!) { areas("
        "filter: {area_name: {match: $name, exactMatch: true}}, limit: 1"
        f") {{ {body} }} }}"
    )


def post(endpoint: str, query: str, variables: dict, attempts: int = 5) -> dict:
    payload = json.dumps({"query": query, "variables": variables}).encode()
    last = None
    for i in range(attempts):
        req = urllib.request.Request(
            endpoint,
            data=payload,
            # The public API 403s urllib's default User-Agent.
            headers={
                "Content-Type": "application/json",
                "User-Agent": "openbeta-mcp-crosscheck/1.0",
            },
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                doc = json.load(resp)
            if doc.get("errors"):
                raise SystemExit(f"GraphQL errors: {doc['errors']}")
            return doc["data"]
        except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError) as e:
            # 502s from the public API are transient; a refused connection to a
            # local stack is not, but retrying it costs only a few seconds.
            last = e
            if i < attempts - 1:
                time.sleep(2 * (i + 1))
    raise SystemExit(f"{endpoint} unreachable after {attempts} attempts: {last}")


def walk(area: dict, path: tuple = ()):
    """Yield (path, area) for every area in the subtree, depth first."""
    here = path + (area["areaName"],)
    yield here, area
    for child in area.get("children") or []:
        yield from walk(child, here)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--area", required=True, help="exact area name to crawl from")
    ap.add_argument("--endpoint", default=DEFAULT_ENDPOINT)
    ap.add_argument("--depth", type=int, default=DEPTH)
    args = ap.parse_args()

    data = post(args.endpoint, build_query(args.depth), {"name": args.area})
    areas = data["areas"]
    if not areas:
        raise SystemExit(f"no area named {args.area!r} at {args.endpoint}")
    root = areas[0]

    print(f"endpoint: {args.endpoint}")
    print(f"root:     {root['areaName']} (totalClimbs={root['totalClimbs']})\n")

    leaves, zeros, exact, wrong_nonzero, truncated = [], [], [], [], []
    for path, area in walk(root):
        children = area.get("children")
        # At the depth limit `children` is absent rather than empty, so a
        # non-leaf with no children key was cut off, not childless.
        if children is None and not area["metadata"]["leaf"]:
            truncated.append(path)
            continue
        if not area["metadata"]["leaf"]:
            continue
        n = len(area.get("climbs") or [])
        if n == 0:
            continue
        leaves.append((path, area["totalClimbs"], n))
        if area["totalClimbs"] == n:
            exact.append((path, n))
        elif area["totalClimbs"] == 0:
            zeros.append((path, n))
        else:
            wrong_nonzero.append((path, area["totalClimbs"], n))

    print(f"leaf areas holding climbs:   {len(leaves)}")
    print(f"  totalClimbs == len(climbs): {len(exact)}")
    print(f"  totalClimbs == 0 (wrong):   {len(zeros)}")
    print(f"  wrong but non-zero:         {len(wrong_nonzero)}")
    print(f"climbs invisible in totalClimbs: {sum(n for _, n in zeros)}")
    if truncated:
        print(f"(!) {len(truncated)} subtrees hit the depth limit; raise --depth")

    if zeros:
        print("\nleaves reporting 0 while holding climbs:")
        for path, n in sorted(zeros, key=lambda z: -z[1])[:15]:
            print(f"  {n:4}  {'/'.join(path)}")

    # A count that is wrong but non-zero would mean the field drifts. Only ever
    # being 0 or exactly right means it is a counter that some write paths never
    # set — a different bug with a different fix, so the distinction is the
    # single most useful thing this script reports.
    if wrong_nonzero:
        print("\nleaves wrong but non-zero (field drifts, not merely unset):")
        for path, t, n in wrong_nonzero[:15]:
            print(f"  totalClimbs={t:5} len(climbs)={n:5}  {'/'.join(path)}")

    return 1 if zeros or wrong_nonzero else 0


if __name__ == "__main__":
    sys.exit(main())
