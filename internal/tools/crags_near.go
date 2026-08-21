package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// MaxCrags caps how many crags a single call returns. A metro-sized search
// yields far more than a model can usefully read, so only the nearest are kept
// and Count reports how many were found.
//
// A var rather than a const so the eval harness can sweep it and measure what
// the cap costs; main.go sets it once at startup, before the first call.
var MaxCrags = 20

// defaultMaxDistanceKm is a radius that covers a climbing destination and its
// surroundings without reaching the next one.
const defaultMaxDistanceKm = 20

// maxDistanceLimitKm bounds the radius. Beyond this the fan-out below stops
// being a reasonable thing to do to a free, volunteer-run API.
const maxDistanceLimitKm = 500

// detailConcurrency limits the fan-out. Sequential is slow and unlimited is
// rude; five is neither.
const detailConcurrency = 5

// CragsNearArgs is the input schema for crags_near.
type CragsNearArgs struct {
	Place         string    `json:"place,omitempty" jsonschema:"A climbing destination or town, for example \"Squamish\" or \"Fontainebleau\". Supply this or lnglat, not both."`
	LngLat        []float64 `json:"lnglat,omitempty" jsonschema:"An explicit search origin as two numbers in the order [longitude, latitude]. Longitude comes first. Use this when the place is not recognised, for example [-123.16, 49.70]."`
	MaxDistanceKm *float64  `json:"maxDistanceKm,omitempty" jsonschema:"Search radius in kilometres. Defaults to 20, maximum 500."`
}

// ResolvedPlace records what the search origin ended up being, so a wrong
// gazetteer entry or a misread coordinate is visible in the result rather than
// silently shifting the answer.
type ResolvedPlace struct {
	Lat    float64 `json:"lat"`
	Lng    float64 `json:"lng"`
	Source string  `json:"source" jsonschema:"How the origin was determined: \"gazetteer\" for a known place name, \"caller\" for supplied coordinates."`
}

// NearbyCrag is one crag in a crags_near result.
type NearbyCrag struct {
	UUID       string   `json:"uuid"`
	Name       string   `json:"name"`
	Lat        float64  `json:"lat"`
	Lng        float64  `json:"lng"`
	DistanceKm float64  `json:"distanceKm"`
	ClimbCount int      `json:"climbCount"`
	IsBoulder  bool     `json:"isBoulder,omitempty"`
	Path       []string `json:"path,omitempty"`
}

// CragsNearResult is the output schema for crags_near.
type CragsNearResult struct {
	Crags    []NearbyCrag  `json:"crags" jsonschema:"Crags holding climbs, most climbs first. Capped at 20."`
	Count    int           `json:"count" jsonschema:"How many crags the radius found, before the nearest 20 were kept and before crags with no climbs were dropped. An upper bound: it counts crags that may hold nothing climbable."`
	Returned int           `json:"returned" jsonschema:"How many crags are in the crags array. At most 20."`
	Origin   ResolvedPlace `json:"origin"`
}

// core: no MCP types, returns a plain error
func cragsNear(ctx context.Context, gql graphql.Client, resolver geo.Resolver, args CragsNearArgs) (CragsNearResult, error) {
	origin, err := resolveOrigin(ctx, resolver, args)
	if err != nil {
		return CragsNearResult{}, err
	}

	point := geo.Point{Lat: origin.Lat, Lng: origin.Lng}
	// Nearest first, capped. Ranking by climb count is not possible here —
	// cragsNear returns no climbs, which is what the fan-out below is for.
	//
	// total is taken before the cap and before the climb filter, because those
	// are the two steps that make len(crags) unable to exceed MaxCrags — which
	// is what the schema had been promising it could.
	found, total, err := nearestCrags(ctx, gql, point, args.MaxDistanceKm)
	if err != nil {
		return CragsNearResult{}, err
	}

	crags, err := withClimbCounts(ctx, gql, point, found)
	if err != nil {
		return CragsNearResult{}, err
	}

	// Densest first: a model reading top-down should meet the significant crags
	// first.
	slices.SortStableFunc(crags, func(a, b NearbyCrag) int {
		return cmp.Compare(b.ClimbCount, a.ClimbCount)
	})

	return CragsNearResult{Crags: crags, Count: total, Returned: len(crags), Origin: origin}, nil
}

// CragsNearCrag is the generated area type cragsNear returns. Aliased because
// the generated name is bound to the operation and unwieldy at every use.
type CragsNearCrag = generated.CragsNearCragsNearCragsArea

// resolveOrigin turns the arguments into a search origin, rejecting anything
// ambiguous before a request is made (FR-18).
func resolveOrigin(ctx context.Context, resolver geo.Resolver, args CragsNearArgs) (ResolvedPlace, error) {
	hasPlace, hasLngLat := args.Place != "", len(args.LngLat) > 0
	switch {
	case hasPlace && hasLngLat:
		return ResolvedPlace{}, errors.New("pass either place or lnglat, not both")
	case !hasPlace && !hasLngLat:
		return ResolvedPlace{}, errors.New("pass a place name, or lnglat as [longitude, latitude]")
	case hasLngLat:
		if len(args.LngLat) != 2 {
			return ResolvedPlace{}, fmt.Errorf("lnglat must have exactly 2 elements [longitude, latitude], got %d", len(args.LngLat))
		}
		lng, lat := args.LngLat[0], args.LngLat[1]
		// geo owns the range check; the ordering hint belongs here, because the
		// order only exists in this tool's array argument and a transposed pair
		// is the mistake it invites.
		p, err := geo.NewPoint(lat, lng)
		if err != nil {
			return ResolvedPlace{}, fmt.Errorf("%w; order is [longitude, latitude]", err)
		}
		return ResolvedPlace{Lat: p.Lat, Lng: p.Lng, Source: "caller"}, nil
	default:
		p, err := resolver.Resolve(ctx, args.Place)
		if err != nil {
			return ResolvedPlace{}, err
		}
		return ResolvedPlace{Lat: p.Lat, Lng: p.Lng, Source: "gazetteer"}, nil
	}
}

// fetchAreaDetails asks for each area's detail concurrently, returning a slice
// positionally matching areas with nil where the call failed.
//
// cragsNear returns no climbs at all, so this second round trip is the only way
// to learn anything about what a crag actually holds — climb counts here, and
// the climbs themselves for find_climbs.
//
// A crag that fails is dropped rather than failing the whole call: upstream
// returns intermittent 502s, and one of them should degrade a result rather
// than erase it.
func fetchAreaDetails(ctx context.Context, gql graphql.Client, areas []CragsNearCrag) ([]*generated.GetAreaDetailsArea, int, error) {
	// Per-index slots rather than shared variables: every goroutine writes only
	// its own element, so no locking is needed to collect either result.
	details := make([]*generated.GetAreaDetailsArea, len(areas))
	errs := make([]error, len(areas))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(detailConcurrency)
	for i, a := range areas {
		g.Go(func() error {
			detail, err := generated.GetAreaDetails(ctx, gql, a.Uuid)
			if err != nil {
				errs[i] = err
				return nil
			}
			details[i] = &detail.Area
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}

	failed := 0
	var firstErr error
	for _, err := range errs {
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return details, failed, firstErr
}

// allFailed turns "every detail call failed" into an error, and anything less
// into nil.
func allFailed(attempted, failed int, firstErr error) error {
	if attempted > 0 && failed == attempted {
		return fmt.Errorf("no crag details could be fetched: %w", firstErr)
	}
	return nil
}

// withClimbCounts fills in the climb count cragsNear cannot provide.
func withClimbCounts(ctx context.Context, gql graphql.Client, origin geo.Point, areas []CragsNearCrag) ([]NearbyCrag, error) {
	details, failed, firstErr := fetchAreaDetails(ctx, gql, areas)
	if err := allFailed(len(areas), failed, firstErr); err != nil {
		return nil, err
	}

	// Non-nil so an empty result marshals as [] rather than null. An empty
	// radius is a valid answer, not an error (FR-11).
	crags := make([]NearbyCrag, 0, len(areas))
	for i, a := range areas {
		if details[i] == nil {
			continue
		}
		n := climbCount(details[i].TotalClimbs, len(details[i].Climbs))
		if n == 0 {
			continue
		}
		crags = append(crags, NearbyCrag{
			UUID:       a.Uuid,
			Name:       a.AreaName,
			Lat:        a.Metadata.Lat,
			Lng:        a.Metadata.Lng,
			DistanceKm: distanceKm(origin, a),
			ClimbCount: n,
			IsBoulder:  a.Metadata.IsBoulder,
			Path:       a.PathTokens,
		})
	}
	return crags, nil
}

// climbCount reports how many climbs an area holds.
//
// Area.totalClimbs is unreliable and cannot be used alone. It reads 0 on most
// leaf crags that plainly have climbs — Tantalus Wall reports totalClimbs 0 with
// 8 climbs, Petrifying Wall 0 with 74. It is only populated meaningfully on
// parent areas, where it aggregates descendants.
//
// So: count climbs for leaf areas, and fall back to totalClimbs for parents,
// whose own climbs array is always empty. See docs/graphql-findings.md §1.
func climbCount(totalClimbs, climbs int) int {
	if climbs > 0 {
		return climbs
	}
	if totalClimbs > 0 {
		return totalClimbs
	}
	return 0
}

func distanceKm(origin geo.Point, a CragsNearCrag) float64 {
	return geo.Haversine(origin, geo.Point{Lat: a.Metadata.Lat, Lng: a.Metadata.Lng})
}

// adapter: MCP signature, no logic
func HandleCragsNear(gql graphql.Client, resolver geo.Resolver) mcp.ToolHandlerFor[CragsNearArgs, CragsNearResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CragsNearArgs) (*mcp.CallToolResult, CragsNearResult, error) {
		out, err := cragsNear(ctx, gql, resolver, args)
		return nil, out, err
	}
}
