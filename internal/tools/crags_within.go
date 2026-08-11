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

// CragsWithinArgs is the input schema for crags_within.
//
// The bbox description carries the element ordering because the upstream schema
// types it as a bare [Float] — nothing validates the order server-side, and a
// transposed pair is the easiest mistake for a caller to make (NFR-12).
type CragsWithinArgs struct {
	BBox []float64 `json:"bbox" jsonschema:"Bounding box as exactly four numbers in the order [minLng, minLat, maxLng, maxLat]. Longitude comes first. Example for Squamish, BC: [-123.2, 49.6, -122.9, 49.8]"`
	Zoom *float64  `json:"zoom,omitempty" jsonschema:"Map zoom level controlling which level of the area hierarchy is returned. 11 or above returns individual crags; below 11 returns larger parent regions. Defaults to 13."`
}

// CragsWithinResult is the output schema for crags_within.
type CragsWithinResult struct {
	Crags []openbeta.CragSummary `json:"crags"`
	Count int                    `json:"count"`
}

func HandleCragsWithin(gqlClient *graphql.Client) mcp.ToolHandlerFor[CragsWithinArgs, CragsWithinResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CragsWithinArgs) (*mcp.CallToolResult, CragsWithinResult, error) {
		bbox, err := openbeta.NewBBox(args.BBox)
		if err != nil {
			return nil, CragsWithinResult{}, err
		}

		zoom := float64(defaultZoom)
		if args.Zoom != nil {
			zoom = *args.Zoom
		}
		filter := generated.SearchWithinFilter{Bbox: bbox[:], Zoom: zoom}
		crags, err := generated.CragsWithin(ctx, *gqlClient, filter)
		if err != nil {
			return nil, CragsWithinResult{}, fmt.Errorf("looking up crags: %w", err)
		}
		// Delete areas with no climbs
		crags.CragsWithin = slices.DeleteFunc(crags.CragsWithin, func(a generated.CragsWithinCragsWithinArea) bool {
			return climbCount(a) == 0
		})
		// Sort by climbCount
		slices.SortFunc(crags.CragsWithin, func(a, b generated.CragsWithinCragsWithinArea) int {
			return cmp.Compare(climbCount(b), climbCount(a))
		})
		// Get top 20 to reduce output for AI
		const maxCrags = 20
		if len(crags.CragsWithin) > maxCrags {
			crags.CragsWithin = crags.CragsWithin[:maxCrags]
		}
		// Drops the climbs array
		out := make([]openbeta.CragSummary, 0, len(crags.CragsWithin))
		for _, a := range crags.CragsWithin {
			// Skips empty
			if !hasClimbs(a) {
				continue
			}
			out = append(out, openbeta.CragSummary{
				UUID:       a.Uuid,
				Name:       a.AreaName,
				Lat:        a.Metadata.Lat,
				Lng:        a.Metadata.Lng,
				ClimbCount: climbCount(a),
				IsBoulder:  a.Metadata.IsBoulder,
				Path:       a.PathTokens,
			})
		}

		return nil, CragsWithinResult{Crags: out, Count: len(out)}, nil
	}
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
func climbCount(a generated.CragsWithinCragsWithinArea) int {
	if n := len(a.Climbs); n > 0 {
		return n
	}
	if a.TotalClimbs > 0 {
		return a.TotalClimbs
	}
	return 0
}

func hasClimbs(a generated.CragsWithinCragsWithinArea) bool {
	return climbCount(a) > 0
}
