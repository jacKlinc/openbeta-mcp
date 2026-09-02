# The golden set

27 cases in `golden-set.jsonl`, one JSON object per line. `manifest.json` records
what the expected values are pinned to: snapshot, endpoint, versions, fingerprints.

For the pipeline that reads these, see [../README.md](../README.md). For the field
definitions, see [`judge/models.py`](../models.py) — the descriptions there are the
reference, and `Case.model_json_schema()` prints them.

## Categories

| | |
| --- | --- |
| `happy_path` | The tool can answer and the answer is checkable. |
| `edge` | The tool answers, but the honest answer is not the obvious one: count-capping, contradictory constraints, scope limits. |
| `no_data` | The query is well formed and the answer is genuinely empty. Grades whether the agent reports that or fills the gap. |
| `honest_failure` | The *data* cannot support the question; the correct answer says so. |
| `no_tool_baseline` | Answerable from the model's own knowledge. **The number the server has to beat.** |

`honest_failure` is a third of the set, deliberately. The real failure mode of a
trimmed response is not that the model cannot answer — it is that it invents
something plausible.

## What the data does not contain

Checked against the tool structs in `internal/tools/` and the upstream schema.
Every row is a question the set asks anyway, as an honest-failure case.

| Asked for | Status |
| --- | --- |
| Approach length or time | Absent everywhere, and not computable: no trailhead geometry, only crag lat/lng. |
| Quality, stars, "classic" | **Not in the upstream schema at all.** |
| Ticks, popularity, traffic | `Climb.ticks` exists upstream but no query selects it. There is no `get_ticks`. |
| Pitch count | `Climb.pitches` is empty on every climb. `find_climbs` infers `multipitch` from `lengthM >= 60`, so single-pitch is not expressible — only multipitch-or-unknown. |
| Protection prose | Not selected. `get_area_details` returns the `safety` enum only. |
| Bouldering, ice, mixed, snow, DWS | Rejected by `find_climbs`: their grade systems are not parsed. |
| British E-grades | `SystemFor` maps `US`, `FR` and `UIAA` only. UK crags land in `skipped`. |

## USA-only

Every case targets a USA place, enforced by the `Case` model. Two independent
reasons:

- The seeded local stack holds USA areas only — Squamish, Peak District, Canmore
  and Skaha all return zero crags.
- `Area.totalClimbs` undercounts badly outside the USA, which corrupts `crags_near`
  ordering and membership since it sorts by climb count and caps at 20. Measured in
  [`docs/findings/totalclimbs/`](../../../docs/findings/totalclimbs/).

Cost: every USA crag is YDS, so the French and UIAA parsers in `internal/grade/`
have unit tests but no case here. Worth stating in the writeup.

## Count honesty

Several cases grade claims the tool descriptions make deliberately:

- `crags_near.count` is an **upper bound** — counted before empty crags are dropped.
- `find_climbs.count` is a **floor** — the search stops at 30.
- `find_climbs.skipped` means routes were dropped because their grade could not be
  read, so the list is not complete.
- `get_area_details` on a parent area returns `totalClimbs` beside an **empty**
  `climbs` array: a rollup over descendants, not routes held here.

A model that repeats any of these as a plain total fails in a way a pass rate will
not show.

## Coverage

| Axis | Values |
| --- | --- |
| Discipline | sport / trad / tr / excluded |
| Grade | 5.6-5.7 / 5.8-5.10a / 5.10a-5.11a / 5.11+ |
| Pitch | multipitch / any |
| Tool | `find_climbs` / `crags_near` / `get_area_details` / chained |
| Geocoding | gazetteer hit / miss / raw lnglat |
| Result state | results / empty / `skipped>0` / count-capped |
| Absent field | approach / quality / popularity |
| Baseline | no tool needed |

## Adding a case

1. Name the capability. If you cannot say what one thing it tests, it is not a case.
2. Check the field is in the data. If it is not, the case is `honest_failure` and
   the expectation inverts.
3. Write `user_input` as natural language naming a gazetteer place. Every tool
   needs `place` or `lnglat`.
4. Prefer a computable expectation; reach for `prose` only when correctness means
   faithfulness, and say why in `why_not_deterministic`.
5. Fill `requires_fields` honestly — it predicts which variants fail.
6. Run `python -m judge.groundtruth` and check the generated value.

The `Case` model rejects a malformed case at load with the field named. Prefer a
case that exposes a specific failure mode over another variation on one covered.

## Method honesty

Worth stating in the writeup:

- N is small.
- The thing being measured was built by the person measuring it.
- A third of the set is honest-failure cases, so the headline pass rate is not
  "how often does it find a good route".

## TODO

- `get_ticks` cases, once the tool exists.
- Fault-injection cases, once a transport wrapper can fail a tool call or strip
  fields. Two were drafted and dropped; the protection half is already covered by
  `trad_first_lead_gunks` against genuinely absent data.
- Realign `GRADE_WINDOW` in `evals/tokens/corpus.py` (5.8-5.11a) with the 5.6-5.12
  scope in [judge.md](../../../docs/plans/judge.md).

### Answered questions

Kept because the answers shaped several cases.

1. **Is "suitable for top roping" assessable?** Partly. `disciplines: ["tr"]` is
   recorded, so "recorded as top-rope" is answerable; anchors and walk-offs are not.
2. **"Short approach"?** Not available and not computable. `distanceKm` is distance
   from the *search origin*, not from parking — reading it as an approach is the
   failure `approach_length_joshua_tree` catches.
3. **"Easy logistics", "suitable for a group"?** Unmeasurable, so they appear only
   in honest-failure cases.
