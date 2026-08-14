# Crags Near Query

The `cragsWithin` query cooks the API so we need to find a better solution

- The hope was that `cragsNear` would be the silver bullet but that also has issues

1. `climbs` returns nothing. This is meant to be a list of all climbs at that `placeId`
2. `children` returns nothing. This is meant to be a list of all children at that `placeId`

### Potential Solution

1. Query `cragsNear` to get a list of climbing UUIDs near an area
2. Loop over each of these with goroutines and use `getAreaDetails` to find more info

Example:

1. Get crags within 5km (5,000m) of Squamish:
    ```json
    {
    "placeId": "squamish",
    "lnglat": {"lat": 49.665393, "lng": -123.253667},
    "maxDistance": 5000,
    "includeCrags": true
    }
    ```
2. Loop over results:
    ```
    for crag in data:
        getAreaDetails(d)
    ```

## Implemented

The `crags_near` tool in [internal/tools/crags_near.go](../../internal/tools/crags_near.go) does the
above, and `crags_within` has been removed. Shape as built:

1. Resolve `place` to coordinates via [internal/geo](../../internal/geo/), or take `lnglat` directly.
2. `cragsNear(lnglat, maxDistance)` — `placeId` is not sent, since the resolver only echoes it back.
3. Sort by haversine distance and keep the nearest 20. Ranking by climb count is not possible here,
   because the counts do not exist yet.
4. Fan out `getAreaDetails` over those 20, five at a time, for the real climb counts.
5. Drop crags with no climbs, sort by climb count, return with distances.

Two things worth remembering when reading that code:

- **`maxDistance` is metres.** The tool's own argument is `maxDistanceKm` and the conversion happens
  once, at the call. Passing `5` where `5000` was meant searches a five-metre radius and returns an
  empty list, which is indistinguishable from "no crags here".
- **A failed detail call drops that crag rather than failing the request.** Upstream returns
  intermittent 502s. If every call fails it is an outage, and that does return an error, because an
  empty list would read as "nothing here".
