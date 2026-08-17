# `totalClimbs` undercounts outside the USA

**Measured 2026-08-17 against `https://api.openbeta.io/graphql`.**

`Area.totalClimbs` is unusable as a climb count outside the USA. British Columbia
reports **1052** climbs while its leaf areas actually hold **8711** — an 88%
undercount. Alberta reports **51** against **2310**, a 98% undercount.

The rollup arithmetic is not the problem. It sums its children exactly. What it
sums is wrong: a large fraction of leaf areas report `totalClimbs: 0` while
returning climbs, and those zeros propagate silently up the tree.

## Reproduce it

No toolchain needed — paste any of these into
[graphiql-online](https://graphiql-online.com) pointed at
`https://api.openbeta.io/graphql`:

| File                                                 | Question                                               |
| ---------------------------------------------------- | ------------------------------------------------------ |
| [`leaf-check.graphql`](leaf-check.graphql)           | Does a leaf's `totalClimbs` match the climbs it holds? |
| [`malamute.graphql`](malamute.graphql)               | Is a reported `0` real?                                |
| [`squamish-rollup.graphql`](squamish-rollup.graphql) | Does the parent sum over its children?                 |
| [`cohort-check.graphql`](cohort-check.graphql)       | When were the broken areas created?                    |

[`crosscheck.py`](crosscheck.py) generalises them over a whole subtree. Stdlib
only:

```
$ ./crosscheck.py --area "British Columbia"
$ ./crosscheck.py --area "Yosemite Valley" --endpoint http://localhost:4000/
```

It exits non-zero when any leaf disagrees, so it doubles as a check for whether
this has since been fixed.

## What we found

### 1. The rollup is faithful

`squamish-rollup.graphql`. Squamish reports 609, and its 31 children sum to
exactly 609. Same at the next level down: Grand Wall Boulders reports 201, and
its 18 children sum to exactly 201.

So nothing is wrong with the aggregation step. Every number below is a
consequence of bad leaf values, not bad arithmetic.

### 2. Leaf values are missing, and missing means zero

`malamute.graphql`. The Malamute reports `totalClimbs: 0`. It has 11 leaf
children, every one reporting `0`, which between them return **61 climbs**:

```
The Cage              totalClimbs=0   climbs=2
Chasing Rainbows      totalClimbs=0   climbs=9
Grub Street           totalClimbs=0   climbs=7
Jacob's Wall          totalClimbs=0   climbs=10
Lower (CLOSED)        totalClimbs=0   climbs=6
Malamute Bouldering   totalClimbs=0   climbs=1
Overly Hanging Out    totalClimbs=0   climbs=1
Quagmire Area         totalClimbs=0   climbs=5
Starr Wall            totalClimbs=0   climbs=14
Stooges Slab          totalClimbs=0   climbs=3
The Terraces          totalClimbs=0   climbs=3
```

Because the rollup is faithful, The Malamute contributes `0` to Squamish's 609.
Those 61 climbs are invisible at every level above them.

`leaf-check.graphql` shows the two states side by side under one parent — of
Grand Wall Boulders' 18 children, 11 report their climb count exactly and 7
report `0` while holding 36 climbs between them.

### 3. The field is never wrong — only ever unset

This is the most useful thing here. Across every subtree crawled below —
several thousand leaf areas — the count of leaves that were **wrong but
non-zero was zero**. Not one. Every leaf either reports its climb count exactly
or reports `0`.

A counter that drifts produces near-misses. A counter that is only ever exact or
absent is a counter some write path never sets. That points at a specific write
path or import, not at gradual staleness, and it means a one-off recompute would
fix the existing data rather than papering over a race.

### 4. It splits on country, not on area size or type

All rows crawled at `--depth 8`, which reaches every leaf in these subtrees.

| Area               | reports | leaves w/ climbs | exact | zero | climbs hidden | true total |
| ------------------ | ------: | ---------------: | ----: | ---: | ------------: | ---------: |
| Yosemite Valley 🇺🇸  |    1505 |              228 |   228 |    0 |             0 |       1505 |
| Red River Gorge 🇺🇸  |    2674 |              196 |   196 |    0 |             0 |       2674 |
| Index 🇺🇸            |     754 |              119 |   119 |    0 |             0 |        754 |
| Kalymnos 🇬🇷         |      18 |                3 |     2 |    1 |             2 |         20 |
| Squamish 🇨🇦         |     609 |              225 |    36 |  189 |          1495 |       2104 |
| Ontario 🇨🇦          |     298 |              318 |    29 |  289 |          2157 |       2455 |
| Alberta 🇨🇦          |      51 |              358 |     4 |  354 |          2259 |       2310 |
| British Columbia 🇨🇦 |    1052 |             1299 |   108 | 1191 |          7659 |       8711 |

Every USA area tried is perfect. Every Canadian area tried is badly wrong. This
is consistent with §3: the USA dataset was populated by something that sets the
counter, and other datasets by something that does not.

### 5. The split is by import cohort, and the current write path is fine

`authorMetadata.createdAt` separates the two groups cleanly:

| Group                          | `createdAt`             | State                    |
| ------------------------------ | ----------------------- | ------------------------ |
| USA leaves (Yosemite, Index)   | 2022                    | all correct              |
| Broken leaves (BC, Alberta)    | **2023 — 100% of them** | all zero                 |
| Any leaf created 2024 or later | 2024–2026               | all correct, none broken |

Of British Columbia's 1191 broken leaves, every single one was created in 2023.
None created in 2024, 2025 or 2026 is broken — 64 of them are correct. Alberta is
the same: all 354 zeros were created in 2023.

So this is not an ongoing fault. Areas written through the current API get a
correct counter. The damage is confined to a 2023 import that loaded non-USA
areas without populating `totalClimbs`, while the 2022 USA import populated it
correctly.

The timestamps are same-day across a batch, which is what makes this look like an
import rather than organic editing. Run [`cohort-check.graphql`](cohort-check.graphql)
— all 11 of The Malamute's children were created on **2023-03-17** and last
updated on **2024-10-06**, to the day:

```
The Cage              totalClimbs=0  climbs=2   created=2023-03-17  updated=2024-10-06
Chasing Rainbows      totalClimbs=0  climbs=9   created=2023-03-17  updated=2024-10-06
Grub Street           totalClimbs=0  climbs=7   created=2023-03-17  updated=2024-10-06
...                                             (all 11 identical)
```

So two batch operations are implicated: the 2023-03-17 import that created these
without a counter, and the 2024-10-06 bulk update that touched them
(`updatedAt` 2024 on 1190 of the 1191 broken BC leaves) without recomputing it.
That second one is the more useful place to look — something already walked
every one of these rows and did not fix the field.

One caveat on the inference: 44 BC leaves created in 2023 *are* correct, so
membership in the 2023 cohort does not guarantee breakage. Those were most
likely edited later in a way that recomputed the field.

A local `openbeta-graphql` stack seeded from the staging dump reports Yosemite
Valley as 228/228 — identical to production. The staging dump is USA-only, so a
local stack **cannot reproduce this bug**; you need the production endpoint or a
dump containing non-USA areas. Worth knowing before trying to chase it locally.

## Why this matters to a client

A client that trusts `totalClimbs` to decide whether an area is worth showing
will discard most non-USA crags. Filtering `totalClimbs > 0` across the Squamish
bbox at zoom 13 returns 180 areas, of which 176 hold at least one climb but only
33 report a non-zero count — the filter throws away 143 real crags, 81% of them.

`cragsNear` makes this sharper, because it returns `totalClimbs` but an empty
`climbs` array, so there is nothing to fall back on. Petrifying Wall comes back
from `cragsNear` as `totalClimbs: 0` while actually holding 74 climbs. This
repo's `crags_near` tool spends one extra request per crag to work around
exactly that.

Our workaround is in [`../../graphql-findings.md`](../../graphql-findings.md) §1
and §4: prefer `len(climbs)`, fall back to `totalClimbs` only when climbs are
unavailable.

## Suggested fix

A one-off backfill, not a code change to the write path. §5 shows areas created
from 2024 onward are consistently correct, so whatever maintains the counter
today works; only the 2023 import cohort is affected and it will not grow.

Recompute `totalClimbs` from the climbs actually attached to each leaf, then
re-roll up. §3 says this is safe to run wholesale rather than targeting the
affected rows — no non-zero value observed was ever wrong, so a recompute cannot
make any area worse.
