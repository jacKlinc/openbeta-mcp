# Server features the eval work turned up

Four changes to the server, none blocking the judge. Ordered by how much they
change what a caller can ask for.

## 1. Grades: YDS only, everywhere

`gradeRange` parses YDS and nothing else, so `find_climbs` rejects every other
system with "YDS only, for example 5.8 or 5.10b"
([find_climbs.go:203-210](../../internal/tools/find_climbs.go#L203-L210)).

The data does not have this limitation: OpenBeta records grades per `GradeType`
— font, french, uiaa and others alongside yds
([yds.go:4](../../internal/grade/yds.go#L4)). The tool advertises a grade filter
that most of the world's recorded grades cannot use.

**The fix is not a conversion table.** Cross-system conversions are contested
and lossy, and a silent one turns a filter into a guess. Parse each system
natively and filter within the system a route is recorded in; where a route's
system differs from the query's, exclude it and say so in the response rather
than converting.

Scope: `internal/grade` gains a parser per system and a `Span` that carries
which system it is in. `FindClimbsArgs` needs no new field if the grade string
identifies its own system (`5.10a` is YDS, `6b+` is French, `E4 6a` is British)
— worth confirming that the notations are unambiguous before relying on it.

Blocks: a non-US corpus, a non-US golden set, and any claim that the tool serves
climbers outside North America.

## 2. Multipitch is inferred, and the inference is thin

The API stores no pitch count, so `multipitch` is derived from route length, and
`multipitchOnly` deliberately includes routes whose length is unrecorded
([find_climbs.go:47](../../internal/tools/find_climbs.go#L47)). Two consequences:

- There is no `singlePitchOnly`. A caller can ask for multipitch-or-unknown, or
  for everything. "Single pitch only" is not expressible.
- A multipitch result set is contaminated by unknowns, at whatever rate the area
  fails to record length.

**The non-US-data theory does not hold up.** First probe against the public API,
one leaf crag each: Burgers and Fries in Squamish (Canada) has 37 of 50 climbs
with a length recorded; the US sample landed on B-1 Boulder in Yosemite, 7
climbs and none — because it is a boulder field, and boulders have no length by
nature. Coverage looks like it tracks discipline and per-area contribution
quality, not country. Worth a proper sweep before acting on it either way.

Options, cheapest first: report the unknown rate in the response so a model can
qualify its answer; add `singlePitchOnly` with the same honest caveat; or treat
length as unreliable and stop inferring.

## 3. `crags_near` count cannot keep its promise

The schema tells the model `count` "may exceed the crags array, which is capped
at 20". The code sets `Count: len(crags)` after truncating to `MaxCrags` *and*
after dropping crags with no climbs
([crags_near.go:99](../../internal/tools/crags_near.go#L99)), so it never can.

A model cannot distinguish twenty crags nearby from five hundred, and the
schema tells it that it can. Either count before truncating, or correct the
schema. Counting before truncating is the useful direction: "20 shown of 341
found" is what a caller needs to know their search was too wide.

## 4. `find_climbs` scans crags it will discard

`MaxClimbs = 30` bounds the returned routes
([find_climbs.go:17-18](../../internal/tools/find_climbs.go#L17-L18)), but the
bound is applied after every crag in the capped list has had its climbs fetched
and filtered. Measured over the cap sweep: raising `MaxCrags` from 20 to 40
leaves the `find_climbs` token p95 flat at 3,761 while upstream requests rise
from 484 to 772.

Checking the bound during the scan is a 60% saving against a free, volunteer-run
API at no cost to the caller. See
[../findings/corpus/README.md](../findings/corpus/README.md) §5.
