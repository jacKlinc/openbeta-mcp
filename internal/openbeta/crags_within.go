package openbeta

import (
	"context"
	"fmt"
	"sort"
)

// BBox is a geographic bounding box in the order the OpenBeta API expects:
// [minLng, minLat, maxLng, maxLat]. Longitude first — the schema types this as a
// bare [Float], so nothing upstream will catch a swapped pair.
type BBox [4]float64

func (b BBox) MinLng() float64 { return b[0] }
func (b BBox) MinLat() float64 { return b[1] }
func (b BBox) MaxLng() float64 { return b[2] }
func (b BBox) MaxLat() float64 { return b[3] }

// Validate rejects malformed boxes before any upstream call (FR-18).
func (b BBox) Validate() error {
	if b.MinLng() < -180 || b.MaxLng() > 180 {
		return fmt.Errorf("longitude out of range: got [%g, %g], must be within [-180, 180]", b.MinLng(), b.MaxLng())
	}
	if b.MinLat() < -90 || b.MaxLat() > 90 {
		return fmt.Errorf("latitude out of range: got [%g, %g], must be within [-90, 90]", b.MinLat(), b.MaxLat())
	}
	if b.MinLng() > b.MaxLng() {
		return fmt.Errorf("minLng (%g) is greater than maxLng (%g); bbox order is [minLng, minLat, maxLng, maxLat]", b.MinLng(), b.MaxLng())
	}
	if b.MinLat() > b.MaxLat() {
		return fmt.Errorf("minLat (%g) is greater than maxLat (%g); bbox order is [minLng, minLat, maxLng, maxLat]", b.MinLat(), b.MaxLat())
	}
	return nil
}

// NewBBox builds a BBox from a slice, enforcing the length (FR-18).
func NewBBox(v []float64) (BBox, error) {
	var b BBox
	if len(v) != 4 {
		return b, fmt.Errorf("bbox must have exactly 4 elements [minLng, minLat, maxLng, maxLat], got %d", len(v))
	}
	copy(b[:], v)
	return b, b.Validate()
}

// leafZoomThreshold is the zoom at which cragsWithin switches from returning
// organizational parent areas to returning individual crags.
//
// Measured against the Squamish bbox [-123.2, 49.6, -122.9, 49.8]: zoom 6–10 all
// return the same 22 non-leaf areas ("Squamish", "Stawamus Chief"); zoom 11 and
// above return 180 leaf crags. The cutover is a property of the upstream
// resolver, not of the bbox.
const leafZoomThreshold = 11

// CragsWithin returns crags inside bbox. zoom controls the granularity of the
// upstream result: below 11 you get parent regions, at 11 and above individual
// crags.
//
// Areas holding no climbs are dropped, since they are noise to a climber-facing
// tool. That test is the length of the climbs array, not totalClimbs — see
// hasClimbs.
func (c *Client) CragsWithin(ctx context.Context, bbox BBox, zoom float64) ([]CragSummary, error) {
	if err := bbox.Validate(); err != nil {
		return nil, err
	}

	var data cragsWithinData
	err := c.execute(ctx, queryCragsWithin, map[string]any{
		"filter": map[string]any{
			"bbox": []float64{bbox.MinLng(), bbox.MinLat(), bbox.MaxLng(), bbox.MaxLat()},
			"zoom": zoom,
		},
	}, &data)
	if err != nil {
		return nil, err
	}

	// Non-nil so an empty result marshals as [] rather than null. An empty box is
	// a valid answer, not an error (FR-11).
	out := make([]CragSummary, 0, len(data.CragsWithin))
	for _, a := range data.CragsWithin {
		if !hasClimbs(a) {
			continue
		}
		out = append(out, CragSummary{
			UUID:       a.UUID,
			Name:       a.AreaName,
			Lat:        a.Metadata.Lat,
			Lng:        a.Metadata.Lng,
			ClimbCount: climbCount(a),
			IsBoulder:  a.Metadata.IsBoulder,
			Path:       a.PathTokens,
		})
	}

	// Densest crags first: with 180 results in a single metro area, an LLM
	// reading top-down should see the significant ones first.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClimbCount > out[j].ClimbCount })
	return out, nil
}

// climbCount reports how many climbs an area holds.
//
// Area.totalClimbs is unreliable and cannot be used for this. It reads 0 on most
// leaf crags that plainly have climbs — Tantalus Wall reports totalClimbs 0 with
// 8 climbs, Neat and Cool reports 0 with 39. Across the Squamish bbox at zoom 13,
// 143 of 176 crags holding climbs report totalClimbs 0. It is only populated
// meaningfully on parent areas, where it aggregates descendants.
//
// So: count climbs for leaf areas, and fall back to totalClimbs for parents,
// whose own climbs array is always empty.
func climbCount(a area) int {
	if n := len(a.Climbs); n > 0 {
		return n
	}
	if a.TotalClimbs > 0 {
		return a.TotalClimbs
	}
	return 0
}

func hasClimbs(a area) bool {
	return climbCount(a) > 0
}
