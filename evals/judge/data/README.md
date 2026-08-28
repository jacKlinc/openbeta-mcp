# Golden Set

TODO:
- [ ] `get_ticks` cases, once the tool exists. Nothing here references it today.
- [ ] Fault-injection cases, once a transport wrapper exists that can fail a named
      tool call or strip fields from a real response. Two were drafted and dropped
      rather than parked in a second file: the set holds cases that run today.
      Note the protection half of the missing-metadata case is already covered
      without fault injection by `trad_first_lead_gunks`, which grades the same
      behaviour against genuinely absent data.
- [ ] Realign `GRADE_WINDOW` in `evals/tokens/corpus.py` (5.8-5.11a) with the
      5.6-5.12 scope in [judge.md](../../../docs/plans/judge.md), so cost and
      quality are measured over the same distribution.

### Questions

Answered against the shipped tools. Kept here because the answers are the reason
several cases are shaped the way they are.

1. **Is "suitable for top roping" possible to assess?** Partly. `find_climbs`
   takes `disciplines: ["tr"]` and `ClimbMatch.disciplines` reports it, so
   "recorded as top-rope" is answerable. "Suitable for a top rope" — anchors,
   whether you can walk to the top — is not in the data. `toprope_group_index`
   grades the first and forbids the second.
2. **"Short approach" would require distance-from-road.** It is not available and
   cannot be computed: no tool returns approach prose, and the only geometry in a
   response is crag lat/lng. `distanceKm` is distance from the *search origin*,
   not from any parking, and reading it as an approach is a specific failure
   `approach_length_joshua_tree` exists to catch.
3. **"Easy logistics", "suitable for a group" — vague.** Unmeasurable, so they are
   never hard requirements. They appear only in honest-failure cases, where the
   correct behaviour is to say so.

### Ideas

1. Use raster data to infer elevation?

## Overview

The golden set evaluates an agent talking to this MCP server through the three
tools it actually exposes: `crags_near`, `find_climbs`, `get_area_details`.

It measures two things:

- **Tool use** — does the agent pick the right tool and pass real parameters?
- **Answer quality** — does it respect the user's constraints, and does it stay
  inside what the data supports?

The second half matters more than it looks. A large fraction of what climbers ask
for is not in OpenBeta at all, and the most valuable cases here are the ones where
the correct answer is "the data does not say".

## Files

| File | What it is |
| --- | --- |
| `golden-set.jsonl` | The cases. One JSON object per line. |
| `manifest.json` | What the expectations are pinned to: snapshot, endpoint, versions, fingerprints. |

JSONL rather than a JSON array or YAML, for the reason the rest of `evals/` uses
it: `evals/tokens/sweep.py` writes JSONL, `evals/common/jsonl.py` reads it, and
`evals/README.md` makes JSONL the source of truth with MLflow as a view. Cases can
be added, diffed and streamed one line at a time.

## Case fields

```
case_id           str    stable; every result row references it
capability        str    the one thing under test; the primary slice axis
category          str    happy_path | edge | no_data | honest_failure | no_tool_baseline
user_input        str    the question, verbatim, in natural language
place             str    gazetteer place the case targets, or null
grade_range       str    e.g. "5.6-5.10a", or null
pitch_filter      str    multipitch | any
expected          obj    tagged on `kind`; see below
requires_fields   list   response paths the answer depends on, e.g. "climbs[].disciplines"
expected_tools    list   tools that should be called
allowed_tools     list   tools that may be called; anything else is a failure
must_include      list   substrings the answer needs
must_not_include  list   fabrications this case is designed to catch
criteria          list   per-case points fed to the one shared rubric
tags              list   free-form slicing
```

`capability` rather than a persona is the primary key. Two beginner scenarios can
exercise completely different capabilities, and it is the capability that a
response-shape change breaks.

**`requires_fields` is the field that makes this a trimming study.** It records
which parts of the response the answer depends on, so you can predict which
variants should fail before running anything. Comparing predicted failures against
actual ones is a stronger result than a table of pass rates, and it catches the
interesting case: a variant that passes without the data it should have needed,
meaning the model guessed and got lucky.

### What is deliberately *not* a field

- **The model.** It belongs in the result row, keyed on `(run_id, case_id)`
  alongside `judge_model` — see [judge.md](../../../docs/plans/judge.md).
  `design.md` runs the same questions across two model sizes and pairs the
  comparison; a model pinned into a case would fork the set and cost the paired
  tests their power. `schema_version` in `manifest.json` versions the *format*.
- **`difficulty`.** An unvalidated guess at task hardness, and not an experimental
  variable. `category` and `capability` slice results usefully; a subjective
  three-point scale does not.
- **`context`.** A structured restatement of `user_input` invites grading the
  restatement rather than the question. What matters is already in `place`,
  `grade_range`, `pitch_filter` and `criteria`.
- **`judge_dimensions`.** Per-case scoring dimensions mean every case has a
  different scoring shape, so scores cannot be aggregated. One shared versioned
  rubric plus per-case `criteria` says the same thing and stays comparable.

## Grading tiers

From [`evals/docs/design.md`](../../docs/design.md), in order of preference. The
judge is the last resort, not the default — most of the set has ground truth
computable straight from the API, and reaching for a judge there adds cost,
latency and a second source of error to something checkable with `==`.

| `expected.kind` | Carries | Graded by | Used for |
| --- | --- | --- | --- |
| `scalar` | `query`, `generated` | Exact match | Counts, empty results |
| `set` | `query`, `generated` | Set F1, **precision and recall reported separately** | Route and area lists |
| `prose` | `why_not_deterministic` | Judge, on groundedness | Everything whose correctness is faithfulness |

`expected` is a discriminated union, so the three shapes cannot be mixed up: a
`prose` case has no `query` to run and no value to compare, and a `set` case must
carry a `query`. `generated` holds `value` and `args_sha` together — a case is
either generated or it is not, and there is no state where one is present without
the other. Every `prose` case must say in `why_not_deterministic` why it is not
gradeable by machine, which stops the judge becoming the lazy default.

Precision and recall stay separate because they are different bugs: a variant that
drops routes is a recall failure, one that invents them is a precision failure,
and averaging hides which is happening. Area names need normalised matching —
"The Smoke Bluffs" and "Smoke Bluffs" are not different answers.

The judge sees the question, the exact tool output the model had, and the answer.
**Not** the expected value: it is checking entailment against the context, not
correctness against the world.

### Generating expected values

Never write a `set` or `scalar` by hand — it will be wrong and it will drift.

```
python -m judge.groundtruth            # generate, write back
python -m judge.groundtruth --check    # verify nothing drifted, write nothing
```

`groundtruth.py` calls the real compiled binary over stdio, the same path the cost
sweep uses, stores the generating query beside the answer, and records a
fingerprint per case in `manifest.json`. `--check` regenerates and compares, so
snapshot rot surfaces as a failed check rather than as a mysteriously falling
score. It also refuses several things outright — see **Guards** below.

### Configuration

Settings come from the environment via `pydantic-settings`, reading the repo `.env`
with real environment variables taking precedence:

| Variable | Default | |
| --- | --- | --- |
| `OPENBETA_ENDPOINT` | `http://localhost:4000/` | Local, unlike the server's own default. The expectations are pinned to the seeded snapshot, so falling back to live would regenerate against different data and break every later `--check`. |
| `OPENBETA_MAX_CRAGS` | unset | Recorded in the manifest so cost-vs-quality per cap is a join, not a re-run. |
| `GRAPHQL_DIR` | `~/repos/openbeta/openbeta-graphql` | Read for `openbeta_graphql_sha`. |

The endpoint is passed explicitly to the server subprocess rather than left to its
own default, so the endpoint the manifest records is the one the calls went to.

## Categories

**`happy_path`** — the tool can answer, and the answer is checkable. Deterministic
grading.

**`edge`** — the tool answers, but the honest answer is not the obvious one.
Count-capping, contradictory constraints, no results, scope limits.

**`no_data`** — the query is well formed and the answer is genuinely empty. Grades
whether the agent reports that or fills the gap.

**`honest_failure`** — the *data* cannot support the question. The correct answer
says so. These are the highest-signal cases in the set: the real failure mode of a
trimmed response is not that the model cannot answer, it is that it invents
something plausible.

**`no_tool_baseline`** — answerable from the model's own knowledge with no server
at all. Run with tools disabled first, per [judge.md](../../../docs/plans/judge.md)
ramp step 0. **This is the number the server has to beat.** If the server does not
beat it, it is not earning its tokens.

## What the data does not contain

Checked against the tool structs in `internal/tools/` and the upstream schema in
`schema/openbeta.graphql`. Every row here is a question the set asks anyway, as an
honest-failure case.

| Asked for | Status |
| --- | --- |
| Approach length or time | Absent from every response, and not computable — there is no trailhead geometry, only crag lat/lng. |
| Quality, stars, "classic" | **Not in the upstream schema at all.** No `rating`, `stars` or `quality` field exists on `Climb` or `Area`. |
| Ticks, popularity, traffic | `Climb.ticks` exists upstream but no query selects it and no tool returns it. There is no `get_ticks`. |
| Pitch count | `Climb.pitches` is empty on every climb. `find_climbs` infers `multipitch` from `lengthM >= 60` and reports `yes`/`no`/**`unknown`** — so single-pitch is not expressible, only multipitch-or-unknown. |
| Protection prose | `Climb.content.protection` is not selected. `get_area_details` returns the `safety` enum only. |
| Bouldering, ice, mixed, snow, DWS | Rejected by `find_climbs` with an explanation: their grade systems are not parsed. |
| British E-grades | `SystemFor` maps `US`, `FR` and `UIAA` only. UK crags land in `skipped`. |

## Two data hazards the set is built around

### `totalClimbs` undercounts outside the USA

[`docs/findings/totalclimbs/`](../../../docs/findings/totalclimbs/) measured it:
British Columbia reports 1052 climbs against 8711 actually held, Alberta 51
against 2310. The rollup arithmetic is faithful; the leaf values it sums are
missing, and missing reads as zero. Traced to a 2023 import of non-USA areas.

`crags_near` mitigates by preferring `len(climbs)` over `totalClimbs` — which
fixes leaf crags but not parent areas, whose `climbs` array is always empty.
Stawamus Chief is exactly that: `totalClimbs: 369`, `climbs: []`, 32 children.

Because `crags_near` sorts by climb count and caps at 20, both the ordering and
the membership of a crag list are corrupted outside the USA. So:

> **Crag-valued `set` expectations may only be pinned to USA places.** Everywhere
> else uses route-level `find_climbs` expectations, which read `climbs` directly
> and never touch `totalClimbs`.

The `Case` model enforces this rather than trusting anyone to remember it.
Verified against the local snapshot with the existing
[`crosscheck.py`](../../../docs/findings/totalclimbs/crosscheck.py): 228 of 228
Yosemite Valley leaves report `totalClimbs == len(climbs)`, zero climbs invisible.

### The local stack is seeded with USA areas only

Squamish, Peak District, Canmore and Skaha all return **zero** crags against
`scripts/dev-up.sh`. A non-USA case run there would grade the model on an empty
result, which tests nothing.

This points the same way as the `totalClimbs` finding above, so the set resolves
both with one rule: **every case is USA-only**, enforced by the `Case` model. There
is enough USA data to cover every capability worth testing, and staying inside that
cohort means no case needs to know which endpoint it is running against — which is
one field and a branch of validation that no longer have to exist.

The one thing lost is grade-system coverage: every USA crag is YDS, so the French
and UIAA parsers in `internal/grade/` are exercised by unit tests but by no case
here. Worth stating in the writeup rather than leaving a reader to find it.

## Guards in `groundtruth.py`

Structural checks run before any network call, so a malformed case fails in
seconds rather than halfway through a sweep:

- Every name in `expected_tools`/`allowed_tools` is a real tool.
- `expected_tools` is a subset of `allowed_tools`.
- Every key in `expected.query.args` is a real parameter of that tool. *(The set
  this file replaced asserted on `style`, `pitches`, `max_grade`, `difficulty`,
  `approach_max_minutes` and `location` — not one of which exists.)*
- Every non-null `place` is in the gazetteer **and** in the USA cohort.
- A `set` or `scalar` carries a query to generate it from; `prose` carries no value.
- A `happy_path` `set` that generates empty is an error, not a pass: the model
  would succeed by saying nothing was found, whatever the response shape.
- `OPENBETA_ENDPOINT` has a scheme — a bare `host:port` otherwise fails deep in the
  transport with an error naming neither the setting nor the value.

Everything except the last two is declarative, on the `Case`, `Expected` and
`Query` models. Pydantic reports the offending field and value, so a malformed case
fails at load rather than as a confusing tool error mid-sweep.

## Adding a case

1. Name the capability first. If you cannot say what one thing it tests, it is not
   a case yet.
2. Check the field is in the data. If it is not, the case is `honest_failure` and
   the expectation inverts.
3. Write `user_input` as natural language, naming a gazetteer place. Every tool
   requires `place` or `lnglat`; a question with no location cannot be answered at
   all, which is only interesting when clarification is the point.
4. Prefer a computable expectation. Reach for `prose` when correctness genuinely
   means faithfulness.
5. Fill `requires_fields` honestly — this is what predicts which variants fail.
6. Put the fabrications the case exists to catch in `must_not_include`.
7. Run `python -m judge.groundtruth` and check the generated value looks right.

Prefer a case that exposes a specific failure mode over another variation on one
already covered.

## Coverage

Axes that are actually measurable, with where they are exercised:

| Axis | Values | Cases |
| --- | --- | --- |
| Discipline | sport / trad / tr / excluded | `sport_first_lead_rumney`, `trad_first_lead_gunks`, `toprope_group_index`, `bouldering_excluded_hueco` |
| Grade | 5.6-5.7 / 5.8-5.10a / 5.10a-5.11a / 5.11+ | first-lead cases, `multipitch_filter_yosemite`, `grade_span_red_river`, `quality_classics_yosemite` |
| Grade system | YDS only — see the USA-only note | `grade_span_red_river` |
| Pitch | multipitch / any | `multipitch_filter_yosemite`, `conflicting_easy_multipitch_rumney` |
| Tool | `find_climbs` / `crags_near` / `get_area_details` / chained | `crags_near_bishop`, `chain_crag_to_area_yosemite` |
| Geocoding | gazetteer hit / miss / raw lnglat | `unknown_place_llanberis`, `empty_result_open_ocean`, `no_coverage_lnglat` |
| Result state | results / empty / `skipped>0` / count-capped | `skipped_routes_honesty`, `count_floor_find_climbs`, `count_upper_bound_crags_near` |
| Absent field | approach / quality / popularity | the `honest_failure` block |
| Data hazard | parent-area rollup vs empty `climbs` | `parent_area_rollup_yosemite` |
| Baseline | no tool needed | the `no_tool_baseline` block |

### Count honesty

Worth its own note, because it grades claims the tool descriptions make
deliberately:

- `crags_near.count` is an **upper bound** — it counts crags before empty ones are
  dropped, so some hold nothing climbable.
- `find_climbs.count` is a **floor** — the search stops at 30.
- `find_climbs.skipped` means routes were dropped because their grade could not be
  read, so the list is not complete.
- `get_area_details` on a parent area returns `totalClimbs` next to an **empty**
  `climbs` array: the count is a rollup over descendants, not routes held here.

A model that repeats any of these as a plain total is failing in a way a pass rate
will not show.

## Method honesty

Worth stating in the writeup rather than leaving for a reader to find:

- N is small.
- The thing being measured was built by the person measuring it.
- The honest-failure cases are a third of the set. That is deliberate — a set
  built only from what the tool does well reports a flattering number — but it
  means the headline pass rate is not "how often does it find a good route".
