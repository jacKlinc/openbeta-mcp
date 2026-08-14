# Feature: place name resolution

Resolving "near Canmore" or "near Squamish" into coordinates for `cragsNear`.

Companion to `openbeta-mcp-plan.md`. Repo: https://github.com/jacKlinc/openbeta-mcp

(Filename spelled `gazetteer`, one z, two t's.)

## Why this is needed

`cragsNear` takes `lnglat: Point` or `placeId`. `placeId` turns out to be
cosmetic: the resolver does `$addFields: { placeId }` and echoes it back
untouched, with a code comment describing it as a hack to make results uniquely
identifiable for Apollo's client-side cache. So coordinates are the only real
input, and every natural-language query has to be resolved to a point first.

Nothing in MCP does this automatically. The model either supplies coordinates
from its own knowledge or calls a tool.

## The failure that shaped this design

First attempt was to use OpenBeta's own `areas(filter: { area_name: ... })` as the
gazetteer. No new dependency, climbing-specific, appealing.

Searching "Squamish" returns this as the first result:

```json
{
  "uuid": "f6ad85b2-6f96-53e7-954a-0e57f62c7aeb",
  "areaName": "Squamish Crack Boulder",
  "totalClimbs": 1,
  "metadata": { "lat": 43.88219, "lng": -103.46699 },
  "pathTokens": [
    "USA", "South Dakota", "Needles Of Rushmore, The",
    "Mount Rushmore National Memorial", "Olton's Shoulder",
    "Oltons Shoulder Bouldering", "Oltons Shoulder Roadside Drainage",
    "Squamish Crack Boulder"
  ]
}
```

A single boulder problem in South Dakota, named after Squamish, roughly 1,600 km
from the real one.

**The general lesson: climbing area names are a bad gazetteer.** Climbers name
routes and boulders after other places constantly. Any name-based lookup over
route data will surface homages, and there is no reason to expect the real place
to rank first.

Two different jobs that were being conflated:

- **"Where is X?"** is geocoding. Use a geocoder.
- **"Find the area called X"** is search. Use `areas`.

Only the first one feeds `cragsNear`.

## Design

### 1. Embedded gazetteer (primary)

A static table of climbing towns and destinations compiled into the binary.
Squamish, Canmore, Bishop, Joshua Tree, Index, Leavenworth, Red River Gorge,
Chamonix, Fontainebleau, Kalymnos, Ceuse, El Chorro, Siurana, Arco, and so on.

- Zero network calls, zero latency, zero dependency, zero failure modes.
- Covers the large majority of real queries.
- Preserves the single-binary, no-config, no-API-key property that makes
`go install` and run actually work.
- Maintenance is low: these places do not move and the list changes rarely.

### 2. Geocoder fallback (long tail)

For anything not in the table.

Keyless options, in preference order:

- **Open-Meteo geocoding** (`geocoding-api.open-meteo.com/v1/search?name=`).
Keyless, free, indexed on towns and cities, which is exactly the shape of
"climbs near X". Returns country and admin1, which matters for validation
below. Best fit.
- **Photon** (Komoot). Keyless, OSM-based, handles fuzzier input.
- **Nominatim direct.** Keyless but wants a real User-Agent and holds to roughly
1 req/s.

**Rejected: geocode.maps.co.** Requires an API key on both `/search` and
`/reverse` (or a Bearer header). It is Nominatim/OSM data underneath, so the key
requirement buys nothing that is not available keyless from the source, and it
breaks zero-config install.

**If a keyed provider is ever added, keep it optional.** Default to keyless, allow
an env var to override for better quality. Zero-config stays the default.

### 3. Model-supplied coordinates (implicit fallback)

Worth being explicit that the model already knows where Canmore is (roughly
51.089, -115.359) and will produce coordinates with no tool at all. The gazetteer
and geocoder buy robustness on obscure input, not basic capability.

Failure mode to guard: for obscure places the model produces plausible but wrong
coordinates, silently. Nothing downstream flags it. This is an argument for the
tool description steering toward explicit resolution rather than letting the model
freelance.

## Disambiguation, when `areas` search is used anyway

`areas` still has a legitimate job ("tell me about the Grand Wall"). When ranking
name matches, these signals are available, strongest first:

1. **Distance from a resolved point (hard filter).** If the place has already been
geocoded, reject any name match more than N km away. The South Dakota result
dies instantly at 1,600 km. This is the single most effective filter and it is
only available because geocoding happens first.
2. **`pathTokens` depth.** Real Squamish sits around depth 3
(`Canada > British Columbia > Squamish`). The false match is depth 8. Shallow
means "major named area", deep means "specific feature inside something else".
Strong signal, cheap to compute, no extra request.
3. **`metadata.leaf == false`.** For "where is X", the answer is usually a
container, not an individual boulder. The false match is a leaf.
4. **Root `pathTokens[0]` against country.** Open-Meteo returns a country, so
`pathTokens[0] == "Canada"` is a direct cross-check. Note this requires the
geocoder, not the user's own location, which the server does not know and
should not assume: asking about Canmore does not imply being in Canada.
5. **`totalClimbs` as tiebreaker.** 609 versus 1 separates these cleanly. Weak on
its own: it biases toward large areas, so a genuinely small crag queried by
name would lose to a bigger one with a similar name. Its reliability is also
still under investigation (it behaves differently at leaf and non-leaf levels).
Fine as a tiebreak, wrong as a primary key.

The ordering matters. Ideas 1 and 2 from the original sketch (`totalClimbs`,
region matching) both work on this specific example, but both are downstream of
having a trustworthy coordinate to compare against. Get that first and the rest
is tidying.

## Caching

Place coordinates are permanently static. Canmore does not move.

This is the one place where caching pays even inside a short-lived stdio session,
because users ask about the same area repeatedly within a conversation. Unlike
crag data (24h TTL under discussion in Stage 8), geocode results can be held
indefinitely.

The embedded gazetteer is, in effect, a cache with infinite TTL shipped at build
time.

## Interaction with the transport decision

Not independent of Stage 7.

Under stdio, each user runs their own binary from their own IP, so a 1 req/s
provider limit is per-user and generous. Hosted, every request comes from one
Lambda and that policy starts to bite. Nominatim direct is a worse bet if hosting
is likely; Open-Meteo is safer either way.

The embedded gazetteer sidesteps this entirely for the common path, which is
another argument for doing it first.

## Tool shape

Open question rather than a decision.

**Separate tool** (`resolve_place`) is more composable but adds a third JSON
Schema sitting in context on every turn, which is the fixed cost flagged as worth
measuring separately in Stage 6.

**Folded into `crags_near`** (accept either `lnglat` or a `place` string, resolve
internally) means one fewer schema in context, one fewer round trip, and the model
cannot forget to geocode first. Cost is a tool doing two things.

Current lean: fold it in, split later if a second consumer appears. Measuring the
schema cost of both shapes is a legitimate Stage 6 experiment rather than
something to settle by argument.

## Checklist

- [x]  Embedded gazetteer table, climbing destinations, compiled in —
[internal/geo/gazetteer.go](../../internal/geo/gazetteer.go)
- [ ]  Keyless geocoder fallback (Open-Meteo first choice) — `geo.Resolver` is the seam it slots
behind, no call-site change needed
- [x]  Indefinite caching of resolved coordinates — the table *is* the cache; revisit when the
geocoder lands
- [ ]  Distance-based rejection for `areas` name matches
- [ ]  `pathTokens` depth and `leaf` as ranking signals
- [x]  Decide tool shape: **folded into `crags_near`**, which takes `place` or `lnglat`. Split later
if a second consumer appears
- [x]  Tool description steers toward explicit resolution rather than model-supplied coordinates:
an unknown place is a typed error naming the place, and the tool description tells the model to
retry with `lnglat`

Note on the last two: a miss is deliberately *not* silent. `ErrPlaceUnknown` says "no coordinates
known for X; pass lnglat instead", which is the documented guard against the model inventing
plausible-but-wrong coordinates for obscure places.

## Related upstream finding

`areas` name search was slow before `limit` was added. Worth confirming whether
`area_name` is indexed: an `exactMatch: true` lookup should be fast if it is, and
the `exactMatch: false` path builds a case-insensitive unanchored regex
(`new RegExp(match, 'ig')`) which can never use an index. If both are slow, the
missing index is a concrete finding to add to the upstream performance report
alongside the `climbs` fan-out one.

Also note `exactMatch: true` is case-sensitive against stored names, so
`"canmore"` will not match `"Canmore"`. Easy to mistake an empty result for a slow
one.