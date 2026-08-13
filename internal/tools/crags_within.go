package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// defaultZoom returns individual crags rather than parent regions.
//
// cragsWithin switches hierarchy level at zoom 11: below it the API returns
// organizational parents ("Squamish"), at 11 and above individual crags
// ("Tantalus Wall"). 13 sits clear of that boundary, and "what can I climb
// here" almost always wants crags.
const defaultZoom = 13

// MaxCrags caps how many crags a single call returns. A metro bbox at leaf zoom
// yields ~180 areas, which is more context than a model can usefully read, so
// only the densest are kept. Count reports the number actually returned, so it
// never exceeds this.
const MaxCrags = 20

// CragsWithinArgs is the input schema for crags_within.
//
// The bbox description carries the element ordering because the upstream schema
// types it as a bare [Float] — nothing validates the order server-side, and a
// transposed pair is the easiest mistake for a caller to make (NFR-12).
type CragsWithinArgs struct {
	BBox []float64 `json:"bbox" jsonschema:"Bounding box as exactly four numbers in the order [minLng, minLat, maxLng, maxLat]. Longitude comes first. Example for Squamish, BC: [-123.2, 49.6, -122.9, 49.8]"`
	// TODO: why is this a float64 pointer? Would int64 not be better
	Zoom *float64 `json:"zoom,omitempty" jsonschema:"Map zoom level controlling which level of the area hierarchy is returned. 11 or above returns individual crags; below 11 returns larger parent regions. Defaults to 13."`
}

// CragsWithinResult is the output schema for crags_within.
//
// Count and len(Crags) differ whenever the box holds more than MaxCrags: Count
// is how many crags are in the box, Crags is the densest slice of them.
type CragsWithinResult struct {
	Crags []openbeta.CragSummary `json:"crags" jsonschema:"The crags returned, densest first. Capped at 20; when count exceeds that, this is only the top slice."`
	Count int                    `json:"count" jsonschema:"How many crags with climbs are in the bounding box. May be larger than the crags array, which is capped at 20 — narrow the bbox or raise the zoom to see the rest."`
}

// core: no MCP types, returns a plain error
func cragsWithin(ctx context.Context, gql graphql.Client, args CragsWithinArgs) (CragsWithinResult, error) {
	bbox, err := NewBBox(args.BBox)
	if err != nil {
		return CragsWithinResult{}, err
	}

	zoom := float64(defaultZoom)
	if args.Zoom != nil {
		zoom = *args.Zoom
	}
	filter := generated.SearchWithinFilter{Bbox: bbox[:], Zoom: zoom}
	crags, err := generated.CragsWithin(ctx, gql, filter)
	if err != nil {
		return CragsWithinResult{}, err
	}
	// Delete areas with no climbs
	crags.CragsWithin = slices.DeleteFunc(crags.CragsWithin, func(a generated.CragsWithinCragsWithinArea) bool {
		return ClimbCount(a) == 0
	})
	// Taken before truncation: Count reports how much is actually there, so a
	// caller seeing count 183 against 20 crags knows it has the top slice.
	count := len(crags.CragsWithin)
	// Sort by climbCount
	slices.SortFunc(crags.CragsWithin, func(a, b generated.CragsWithinCragsWithinArea) int {
		return cmp.Compare(ClimbCount(b), ClimbCount(a))
	})
	// Get top MaxCrags to reduce output for AI
	if len(crags.CragsWithin) > MaxCrags {
		crags.CragsWithin = crags.CragsWithin[:MaxCrags]
	}
	// Drops the climbs array
	out := make([]openbeta.CragSummary, 0, len(crags.CragsWithin))
	for _, a := range crags.CragsWithin {
		out = append(out, openbeta.CragSummary{
			UUID:       a.Uuid,
			Name:       a.AreaName,
			Lat:        a.Metadata.Lat,
			Lng:        a.Metadata.Lng,
			ClimbCount: ClimbCount(a),
			IsBoulder:  a.Metadata.IsBoulder,
			Path:       a.PathTokens,
		})
	}

	return CragsWithinResult{Crags: out, Count: count}, nil
}

// adapter: MCP signature, no logic
func HandleCragsWithin(gql *graphql.Client) mcp.ToolHandlerFor[CragsWithinArgs, CragsWithinResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CragsWithinArgs) (*mcp.CallToolResult, CragsWithinResult, error) {
		out, err := cragsWithin(ctx, *gql, args)
		return nil, out, err
	}
}

// ClimbCount reports how many climbs an area holds.
//
// Area.totalClimbs is unreliable and cannot be used for this. It reads 0 on most
// leaf crags that plainly have climbs — Tantalus Wall reports totalClimbs 0 with
// 8 climbs, Neat and Cool reports 0 with 39. Across the Squamish bbox at zoom 13,
// 143 of 176 crags holding climbs report totalClimbs 0. It is only populated
// meaningfully on parent areas, where it aggregates descendants.
//
// So: count climbs for leaf areas, and fall back to totalClimbs for parents,
// whose own climbs array is always empty.
func ClimbCount(a generated.CragsWithinCragsWithinArea) int {
	if n := len(a.Climbs); n > 0 {
		return n
	}
	if a.TotalClimbs > 0 {
		return a.TotalClimbs
	}
	return 0
}

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
