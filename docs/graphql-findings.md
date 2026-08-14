# GraphQL findings — verified against the live API

Evidence behind the query and filter choices in `internal/openbeta`. Everything here was confirmed
by introspection or live query against `https://api.openbeta.io/graphql`, not read from
documentation. Reproduce with `OPENBETA_LIVE=1 go test ./internal/openbeta -run Live -v`.

Reference bbox throughout: Squamish, `[-123.2, 49.6, -122.9, 49.8]`.

---

## 1. `totalClimbs` is not a climb count

This is the most consequential finding, and it inverts the filter originally specified in
[plan.md](poc/plan.md).

`Area.totalClimbs` reads `0` on the large majority of leaf crags that hold climbs:

| Area                       | `totalClimbs` | `len(climbs)` | `leaf` |
| -------------------------- | ------------- | ------------- | ------ |
| Neat and Cool              | 0             | 39            | true   |
| Fern Hill                  | 0             | 31            | true   |
| Slhanay                    | 0             | 29            | true   |
| Shannon Falls Wall         | 0             | 23            | true   |
| Tantalus Wall              | 0             | 8             | true   |
| Cirque of the Uncrackables | 0             | 7             | true   |
| The Apron                  | 51            | 51            | true   |
| Apron Boulders             | 55            | 55            | true   |

Across the Squamish bbox at zoom 13: **180 results, 176 hold at least one climb, only 33 report
`totalClimbs > 0`.** A `totalClimbs == 0` filter discards 143 real crags — 81% of the useful result
set.

Each field is reliable exactly where the other is not:

- **Leaf crags** — `climbs` is populated, `totalClimbs` is usually 0.
- **Parent areas** — `climbs` is always `[]`, `totalClimbs` aggregates descendants.

Hence `climbCount` in [crags_near.go](../internal/tools/crags_near.go): prefer
`len(climbs)`, fall back to `totalClimbs`.

The cost is requesting `climbs { uuid }` inside `cragsWithin`, which takes the query from ~130ms to
~1.2s. `cragsWithin` returns `[Area]`, so this is one round trip, not N+1.

## 2. `zoom` selects hierarchy depth, with a hard cutover at 11

Same bbox, varying zoom:

| zoom | results | `leaf: true` | `leaf: false` |
| ---- | ------- | ------------ | ------------- |
| 6    | 22      | 0            | 22            |
| 8    | 22      | 0            | 22            |
| 9    | 22      | 0            | 22            |
| 10   | 22      | 0            | 22            |
| 11   | 180     | 180          | 0             |
| 13   | 180     | 180          | 0             |

Below 11 you get organizational parents ("Squamish", "Stawamus Chief"); at 11 and above you get
individual crags. Nothing in between, and no mixing of the two.

The tool built on this is gone — `crags_within` was replaced by `crags_near`, which searches by
point and radius and returns leaf crags only, so it never has to pick a hierarchy level. The finding
stands as a property of the API.

This corrects plan.md's reading. plan.md observed 21 results with 9 organizational parents and
concluded that `cragsWithin` "returns area-hierarchy nodes at any level" that must be filtered
apart. That observation was taken at low zoom, where parents are *all* you get — they are what the
API returns instead of leaf crags, not noise mixed in among them.

## 3. An area has climbs or children, never both

Stawamus Chief, `8f267065-fc1a-59ce-bcf1-6e9335548363`:

```
totalClimbs: 369    climbs: []    children: 32    metadata.leaf: false
```

Climbs live only on leaf areas. Reading `climbs` alone reports a 369-route wall as empty, so
`get_area_details` selects both and callers descend via `children`.

Note that `leaf: true` does not imply climbs are present: Western Dihedrals is a leaf with
`climbs: []`.

## 4. `cragsNear` exists, but returns no `climbs` and no `children`

plan.md records that no point/radius resolver exists on the schema. It does:

```graphql
cragsNear(placeId: String, lnglat: Point, minDistance: Int = 0, maxDistance: Int = 48000, includeCrags: Boolean = false): [CragsNear]
input Point { lat: Float, lng: Float }
```

A point/radius search is a better fit for "what can I climb near here" than a synthesized bbox, and
unlike `cragsWithin` it never returns organizational parents — every result is `leaf: true`. But the
sub-documents come back empty.

Squamish, `lnglat {49.665393, -123.253667}`, `maxDistance: 5000`, `includeCrags: true` — 40 crags,
of which **0 have `climbs` and 0 have `children`**. They arrive as `[]`, not `null`, so there is no
`errors` array to notice and no way to select around it. Same areas, same moment, via `area(uuid:)`:

| Area            | via `cragsNear` | via `area(uuid:)` |
| --------------- | --------------- | ----------------- |
| Petrifying Wall | `climbs: []`    | 74 climbs         |
| Woodstock       | `climbs: []`    | 12 climbs         |

Populated on each result: `uuid`, `areaName`, `pathTokens`, `metadata`, and `totalClimbs` — which
carries finding 1's unreliability with it. Petrifying Wall reports `totalClimbs: 0` while holding 74
climbs.

The consequence is that **`cragsNear` cannot distinguish a real crag from an empty one.** The
`len(climbs) > 0` test finding 1 depends on is unavailable, and `totalClimbs` is not a substitute.
Filtering empty areas has to happen after a second call, not within the search — see
[cragsNear/README.md](cragsNear/README.md) for the two-step shape that follows from this.

`includeCrags` defaults to `false`, and with it unset `crags` is empty regardless of radius, so it
is worth passing explicitly.

## 5. Endpoint and error shapes

- The endpoint is `https://api.openbeta.io/graphql`. The bare origin answers trivial queries but
  returns a plain-text `error code: 502` for larger ones.
- GraphQL errors arrive as HTTP 200 with an `errors` array — they must not be read as empty results.
- Malformed queries can return a non-JSON body, so status is checked before parsing.
- `area(uuid:)` returns `data.area: null` for an unknown UUID rather than an error.

## 6. Schema details worth recording

`SearchWithinFilter` types `bbox` as a bare `[Float]`, so a transposed pair is not caught upstream.
Order is `[minLng, minLat, maxLng, maxLat]` — longitude first. `BBox.Validate` catches the common
lat/lng transposition via the coordinate range check.

`ClimbType` fields are `bouldering` (not `boulder`) and `tr`, alongside `sport`, `trad`, `alpine`,
`aid`, `ice`, `mixed`, `snow`, `deepwatersolo`.

`GradeType` carries `vscale`, `yds`, `ewbank`, `french`, `brazilianCrux`, `font`, `uiaa`, `wi`.

## 7. Missing values are encoded in-band, not as null

`Climb` uses placeholder values rather than nulls where data is absent:

| Field | Placeholder |
| --- | --- |
| `safety` | `"UNSPECIFIED"` |
| `fa` | `"unknown"` |
| `length` | `-1` |

All three are normalized to zero values and dropped via `omitempty`. Observed on Apron Boulders,
where most problems carry `fa: "unknown"` and `length: -1`.
