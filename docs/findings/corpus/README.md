# The token corpus measured the wrong things

**Found 2026-08-19 reviewing the MLflow exports of `tok-3700e95` (144 calls per
fan-out tool); rebuilt and re-measured the same day against a local stack.**

Two defects in the corpus, what replaced it, what the `MaxCrags` sweep showed,
and the limits of what MLflow can display. None of it is a bug in the server —
though §3 turns up a schema promise the code cannot keep.

## 1. `maxDistanceKm` is not a cost knob

`nearestCrags` sorts candidates by distance ascending, then truncates to
`MaxCrags = 20` ([find_climbs.go:192-198](../../../internal/tools/find_climbs.go#L192-L198)).
The twenty kept are the twenty nearest, so widening the radius only appends
candidates the truncation discards.

Wherever ≥20 crags sit inside the smallest radius, the 5, 20 and 50 km calls
return the same set in the same order — identical payloads, identical token
counts. The radius still selects *which* crags; it cannot move cost.

The upstream query does widen (`int(km*1000)`), so the API does more work for
nothing visible. Round trips cannot show this either: the fan-out runs over the
already-truncated list, which is why the count is 21 (one search, twenty
details) and never more.

**Consequence.** Crossing every gazetteer place with three radii buys one
measurement three times for every dense origin.

## 2. Half the corpus measures a constant

Empty results, of 144 calls per tool:

| Tool          | Empty |
| ------------- | ----- |
| `crags_near`  | 60+   |
| `find_climbs` | 80+   |

Every empty call returns the same ~31 tokens, so those calls do not widen the
distribution — they pile mass on a single value. This is what makes the token
distribution bimodal, and it is why the reported p50 of 118 describes no call
that ever happened.

**The p50 is an artefact of the corpus, not a property of the tools.**

## 3. What earns a place in the corpus

An origin belongs in the corpus if it puts the tool in a state that costs
something different. Everything else is a duplicate. Density is the thing to
sample, not places crossed with radii — but density is a property of a *place
and a radius together*, not of a place, which is what the rebuild had to learn
the hard way.

**Crags come in clumps.** Measured across the 18 US destinations at nine radii
from 1 to 50 km, only 17 of 162 place-radius pairs hold between 1 and 19 crags,
and every one of those sits at 8 km or under. Estes Park goes from 2 crags at
2 km to 55 at 3 km; Lander is empty out to 10 km, then 75 at 20. A ladder
starting at 5 km — which is where the old corpus started — measures the
saturated case almost everywhere.

So each place is walked up a radius ladder and contributes the rungs whose
payloads genuinely differ:

- **The settled radius**, the first rung at the plateau count. Every wider
  radius returns the same nearest crags.
- **One rung below it**, carrying strictly fewer crags. This has to be strictly
  fewer: "the last radius that was still growing" already holds the plateau's
  count — holding it is what makes the next rung a plateau — so that pair comes
  back byte-identical.
- **A handful of known-empty rungs**, five, pinned. An empty result is a real
  cost worth recording; sixty of them is not.

Nine of the 18 places have no second state at all: they are saturated at 1 km
and contribute one call.

**Count does not mean what it looks like.** `crags_near` reports
`count: len(crags)` after truncating to `MaxCrags` *and* after dropping crags
with no climbs, so a saturated place can report any number at or below the cap —
Smith Rock reports 18 from 1 km to 50 km. A count below the cap is therefore not
evidence the cap is loose, and the schema's promise that it "may exceed the
crags array" is one the code cannot keep. Whether the count changes with the
radius is the signal; the count alone is not.

### Popular areas are the normal case

This tool gets used by people asking about places they have not been, mostly at
beginner to intermediate level. Popular, well-mapped areas are therefore the
representative case, and obscure remote ones are a tail to sample thinly on
purpose.

That is a scoping decision, not convenience, and it is written down here so it
reads as one.

### All 18 US destinations, not three anchors

The plan was three areas at different densities. The ladder made that
unnecessary: it finds each place's own transition, so a dense place and a thin
one both contribute their distinct states and neither needs choosing in advance.
All 18 US names in the gazetteer are in, at 32 calls between them.

`get_area_details` still needs one deep tree, and Yosemite Valley is it — 80
areas crawled breadth-first from the valley and its four largest children,
spanning 322 to 1505 climbs.

## 4. What the corpus excludes, and why

**Non-US areas.** A large fraction carry missing leaf data
([totalClimbs finding](../totalclimbs/README.md),
[openbeta-graphql#489](https://github.com/OpenBeta/openbeta-graphql/issues/489)).
An area whose climbs are invisible returns a smaller payload, so the bad data is
a cost confound as well as a correctness one. Restricting to clean US data
measures the fully-populated, expensive case, which is the conservative choice
for a cost budget.

**UK ticks as a corpus.** `gradeRange` parses YDS only, and anything else errors
with "YDS only, for example 5.8 or 5.10b"
([find_climbs.go:203-210](../../../internal/tools/find_climbs.go#L203-L210)). A
UK corpus can only call `find_climbs` with the grade filter omitted — the cheap
path, and the least worth measuring. That the tool advertises a grade filter
most of the world's recorded grades cannot use is a separate finding worth
writing up.

**Query realism.** A tick list is a good sample of what a real climber asks, and
belongs in its own smaller run answering "what does a typical session cost".
This corpus answers "what can this tool cost". Mixing the two is how the current
one drifted.

## 5. What `MaxCrags` costs

The cap exists because twenty is what a model can usefully read
([crags_near.go:18-20](../../../internal/tools/crags_near.go#L18-L20)). Raising
it so the radius does something would be tuning the product to suit the
measurement. Sweep it instead: `MaxCrags` is settable through
`OPENBETA_MAX_CRAGS` or `-max-crags`, defaults to the shipped 20, and lands in
MLflow as a run param.

Three sweeps of the rebuilt corpus, local stack. p95 is the figure to budget
against — the median flatters a tool whose job is occasionally to return a lot.

| `MaxCrags` |                         | `crags_near`  | `find_climbs` | `get_area_details` |
| ---------- | ----------------------- | ------------- | ------------- | ------------------ |
| 10         | tokens p50 / p95        | 884 / 1,076   | 1,221 / 3,376 | 513 / 3,519        |
| 20         |                         | 1,580 / 2,078 | 1,858 / 3,761 | 513 / 3,519        |
| 40         |                         | 2,157 / 3,878 | 2,473 / 3,761 | 513 / 3,519        |
| 10         | round trips p95 / total | 11 / 286      | 11 / 286      | 1 / 80             |
| 20         |                         | 21 / 484      | 21 / 484      | 1 / 80             |
| 40         |                         | 41 / 772      | 41 / 772      | 1 / 80             |
| 10         | latency p95 (ms)        | 57            | 40            | 6                  |
| 20         |                         | 62            | 73            | 6                  |
| 40         |                         | 96            | 98            | 6                  |

`get_area_details` takes no part in the cap and does not move, which is the
control: the sweep is not perturbing anything it should not.

**The two tools respond differently, and that is the finding.**

`crags_near` is linear — roughly 90-100 tokens per crag admitted, p95 rising
1,076 → 2,078 → 3,878. The cap is the cost dial for this tool.

`find_climbs` saturates. Its p95 goes 3,376 → 3,761 → 3,761, flat from 20 to 40,
while the round trips behind it nearly triple, 484 → 772. `MaxClimbs = 30`
([find_climbs.go:17-18](../../../internal/tools/find_climbs.go#L17-L18)) bounds
the returned routes, so past a point the extra crags are scanned, their climbs
fetched and filtered, then discarded before the payload is built. Doubling the
cap buys a `find_climbs` caller nothing at the tail and costs the API 60% more
requests.

## 6. Run it locally

Not to save money — to afford the call volume a density probe and a cap sweep
both need. [scripts/dev-up.sh](../../../scripts/dev-up.sh) brings the stack up
and `OPENBETA_ENDPOINT` redirects the server; nothing new to build.

```bash
./scripts/dev-up.sh                                            # mongo + graphql on :4000
OPENBETA_ENDPOINT=http://localhost:4000 \
  uv run --project evals python -m tokens.corpus --probe       # rebuild corpus/origins.json
OPENBETA_ENDPOINT=http://localhost:4000 scripts/tokens.sh --delay 0
```

A full sweep takes 4 seconds locally against 357 paced calls on the public API.
The pacing in `tokens.sweep` exists for a volunteer-run service and is pointless
here, hence `--delay 0`.

**What the local data is.** `seed-db.sh` restores a real OpenBeta staging dump,
not synthetic data, so payload sizes are realistic. It is US-only in the copy
used here — a `cragsNear` around Squamish returns nothing — which is one reason
the crawl seeds moved to Yosemite. Staging is not production, so a public-API
run stays the level baseline and local runs are for turning knobs.

It also makes one experiment possible that the public API cannot: seed two local
datasets, one filtered to exclude the updates from the broken period in #489 and
one not, and measure the cost difference. That quantifies how much the missing
leaf data flatters the numbers. It is a finding about OpenBeta rather than a
corpus decision, and belongs beside the `totalClimbs` one.

Two things to plan around:

- **Token counts become seed-dependent.** Cost tracks payload content — names,
  descriptions, ids. A synthetic seed gives the right shape and the wrong level,
  so local numbers cannot be quoted against the public-API baseline.
  `data/tokens/cache/` holds real recorded payloads to check the seed's size
  profile against.
- **Log the endpoint as an MLflow param.** Without it a local run and a public
  run sit in one experiment looking comparable when they are not.

## 7. Did the rebuild work

Measured on the local stack at the shipped cap, `crags_near`:

|                                                       | Old corpus | Rebuilt    |
| ----------------------------------------------------- | ---------- | ---------- |
| calls per fan-out tool                                | 144        | 32         |
| empty results                                         | 60+        | 5 (pinned) |
| p50                                                   | 118        | 1,580      |
| places where a wider radius changed the payload       | 0          | 9 of 18    |
| places returning byte-identical payloads across radii | most       | 0          |

The p50 now sits inside a populated region instead of in the gap between the
empty mode and the full one, and the calls that remain are 32 distinct states
rather than 144 mostly-repeats.

The other nine places are saturated at 1 km and contribute a single call each.
That is not a gap in the corpus — it is the finding, measured: for half the
well-known US destinations, no radius a caller can choose makes any difference
to what `crags_near` returns.

## 8. Conclusion: on to the judge

**Leave the cap at 20 and do not optimise tokens yet.** Three reasons.

**4k tokens is not a problem worth solving.** A p95 of 3.8k on the most
expensive tool is a small fraction of any modern context window, and these tools
are called a handful of times per conversation, not in a loop.

**A cost curve is useless without a quality curve.** This sweep says what each
cap costs. It cannot say what each cap is worth, and the question that matters —
does a model answer better with 40 crags than with 10? — is what the judge
measures. The prize is real: if 10 answers as well as 20, the cap halves the
token cost and cuts upstream load 41% on a volunteer-run API.

**Latency is not in contention.** 40-98 ms p95 across every tool and cap.

The one experiment worth queueing needs both halves: the same 10/20/40 sweep
with the judge scoring the answers, giving cost against quality per cap. The
sweep side of that is already built.

Two things this turned up, neither blocking:

- **`find_climbs` could stop scanning early.** It has a `MaxClimbs = 30` bound
  it could check while scanning rather than after, saving upstream requests at
  no cost to the caller. A server change, not an eval one.
- **`crags_near`'s `count` cannot keep its promise**, as in §3.

## What MLflow can and cannot show

Tested against the running server, not assumed:

| Chart                    | Verdict                                                          |
| ------------------------ | ---------------------------------------------------------------- |
| Quantile curve           | Works — per-call metrics logged with `step` = rank               |
| Series colour by tool    | No — colour follows the *run*; tools differ only by dash pattern |
| Grouped bar, p50/95/99   | No — bar charts take one metric with bars as runs                |
| Parallel coordinates     | Works — several metrics as axes, one line per run                |
| Histogram                | No form for it                                                   |
| Cost vs. `maxDistanceKm` | No — the radius lives in `args` and is never logged as a metric  |

The last two are why [evals/common/plots.py](../../../evals/common/plots.py)
exists. `charts.json` was dropped: nothing read it, and
[evals/docs/charts.md](../../../evals/docs/charts.md) says the same thing where
people look.

Scalars per tool were cut to `n` and p50/p95/p99. Mean, max and sum are
recoverable from the JSONL, and MLflow renders one chart card per metric name,
so each extra scalar is another flat bar to scroll past. The mean was the one
worth losing — bimodal data puts it in the empty gap between the modes.
