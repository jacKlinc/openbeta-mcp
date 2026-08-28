# Golden set and judge

Measuring whether the server helps a model answer climbing questions, as opposed
to what it costs. Cost is settled for now — see
[../findings/corpus/README.md](../findings/corpus/README.md) §8.

## Scope

**Grades 5.6 to 5.12, US, YDS.** Roughly 90% of climbers by the distribution of
reported hardest grades, and the only system the server parses at all (see
[server-features.md](server-features.md) §1). Worth realigning `GRADE_WINDOW` in
`evals/tokens/corpus.py` from 5.8-5.11a to the same range, so cost and quality
are measured over the same distribution.

**Pitches: multipitch-or-unknown, versus unfiltered.** Not "single versus
multi". The server has no `singlePitchOnly` and infers multipitch from length,
so a case asking for single-pitch routes is asking for something the tool cannot
express.

**Discipline axis, for roped rock only.** Superseded as of `a2a3c93`:
`FindClimbsArgs` now takes `disciplines` and `ClimbMatch` returns one, over
`sport`, `trad`, `alpine`, `aid` and `tr`. A trad-only or top-rope-only case is
answerable and gradeable, and the set exercises all three.

What stays out of scope is everything the grade parsers cannot read — bouldering,
deep water solo, ice, mixed, snow. `find_climbs` rejects those by name with the
reason, so the honest-failure framing moves rather than disappears: the right
behaviour is relaying that rejection, not guessing from route names.

**Fixed local data.** The golden set is pinned to the seeded local stack, so
expected answers cannot rot underneath it. Record which dump in the case file.

## Case schema

```
case_id          str        stable, referenced by every result
user_input       str        what the user asks
category         str        happy_path | edge | no_data | honest_failure
grade_range      str        e.g. "5.6-5.10a"; the slice axis, so results group by it
pitch_filter     str        multipitch | any
expected_tools   list[str]  should be called
allowed_tools    list[str]  may be called; anything else is a failure
must_include     list[str]  substrings or facts the answer needs
must_not_include list[str]  fabrications this case is designed to catch
criteria         list[str]  case-specific points fed to the shared rubric
```

`expected_answer` as free text is a trap — the judge ends up grading paraphrase.
`must_include` plus `criteria` says the same thing in a gradeable form.

One shared rubric, versioned, with per-case `criteria`. Forty free-text rubrics
drift apart and scores stop being comparable across cases.

**Rubric must reward hedging.** Grades and protection notes in OpenBeta are
user-contributed opinions. A confident wrong answer should score below a correct
one that says to verify against a current guidebook.

## Result schema

Keyed on `(run_id, case_id)`.

```
run_id, case_id
model                str
judge_model          str    judge drift silently invalidates a series
rubric_version       str    a rubric edit breaks comparability like a case edit
harness_version      str
tool_server_version  str
endpoint             str    matches the cost runs
max_crags            int    so cost-vs-quality per cap is a join, not a re-run
http_round_trips     obj
tokens               obj
criterion_scores     dict   per criterion, not just the aggregate
judge_score          float
judge_reason         str
final_pass           bool
```

## Ramp

0. **No-tool baseline.** Same questions, tools disabled. If the model answers as
   well from memory, the server is not earning its tokens. This is the number
   the whole eval exists to beat, and it costs nothing to collect first.
1. Write 20 cases.
2. **Hand-label those 20 pass/fail before the judge sees them.** Calibration
   needs something to calibrate against. Then measure judge-human agreement, and
   run each case through the judge three times to check it agrees with itself —
   a judge that does not cannot be calibrated.
3. Another 20 cases.
4. Edge cases: unanswerable counts (`count` cannot exceed `MaxCrags`, so "how
   many crags near Bishop" has no truthful answer), empty results, places
   outside the gazetteer, grades outside the range.
5. Freeze the cases. Version the rubric separately — rubric edits are the ones
   made casually and they invalidate comparisons just as thoroughly.
6. Run, and join against the cost runs on `max_crags` for cost against quality.
