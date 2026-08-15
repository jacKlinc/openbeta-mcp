package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
	"github.com/jacKlinc/openbeta-mcp/internal/grade"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// MaxClimbs caps the returned routes. Count reports how many matched.
const MaxClimbs = 30

// multipitchMinLength is the length, in metres, at or above which a route is
// treated as multi-pitch.
//
// This is inference, not data: Climb.pitches exists in the schema and is empty
// on every climb — The Apron returns 51 climbs with no pitch data at all,
// Diedre (6 pitches) among them. Length is the only signal left, and Squamish
// trad lengths separate cleanly: singles run 15-45m, then the multi-pitch
// routes appear at 61/91/121/152m, which are 200/300/400/500ft imports. A rope
// length is the natural boundary between them.
const multipitchMinLength = 60

// Pitch classifications. Three values rather than a bool because length is -1
// on a good fraction of routes — 9 of 59 Squamish trad climbs — and "not
// recorded" is not the same answer as "single pitch".
const (
	PitchesYes     = "yes"
	PitchesNo      = "no"
	PitchesUnknown = "unknown"
)

// FindClimbsArgs is the input schema for find_climbs.
//
// TODO: trad only for now. Sport, boulder and the rest need the same filter
// with a discipline argument, and boulders need V-scale rather than YDS.
type FindClimbsArgs struct {
	Place          string    `json:"place,omitempty" jsonschema:"A climbing destination or town, for example \"Squamish\". Supply this or lnglat, not both."`
	LngLat         []float64 `json:"lnglat,omitempty" jsonschema:"An explicit search origin as two numbers in the order [longitude, latitude]. Longitude comes first."`
	MaxDistanceKm  *float64  `json:"maxDistanceKm,omitempty" jsonschema:"Search radius in kilometres. Defaults to 20, maximum 500."`
	MinGrade       string    `json:"minGrade,omitempty" jsonschema:"Lowest YDS grade to include, for example \"5.8\". Omit for no lower bound."`
	MaxGrade       string    `json:"maxGrade,omitempty" jsonschema:"Highest YDS grade to include, for example \"5.10b\". Omit for no upper bound."`
	MultipitchOnly bool      `json:"multipitchOnly,omitempty" jsonschema:"Return only routes that are multi-pitch or whose length is unrecorded. The API stores no pitch count, so this is inferred from route length."`
}

// ClimbMatch is one route in a find_climbs result.
type ClimbMatch struct {
	Name       string   `json:"name"`
	UUID       string   `json:"uuid"`
	Grade      string   `json:"grade" jsonschema:"The YDS grade exactly as recorded upstream, which may be imprecise, for example \"5.10\" or \"5.11a/b\"."`
	Multipitch string   `json:"multipitch" jsonschema:"\"yes\", \"no\", or \"unknown\" when the route's length is not recorded. Inferred from length, not from a pitch count."`
	LengthM    int      `json:"lengthM,omitempty" jsonschema:"Route length in metres. Absent when not recorded."`
	Crag       string   `json:"crag"`
	CragUUID   string   `json:"cragUuid"`
	DistanceKm float64  `json:"distanceKm"`
	Path       []string `json:"path,omitempty"`
}

// FindClimbsResult is the output schema for find_climbs.
type FindClimbsResult struct {
	Climbs       []ClimbMatch  `json:"climbs" jsonschema:"Matching routes, nearest crag first. Capped at 30."`
	Count        int           `json:"count" jsonschema:"How many routes matched. May exceed the climbs array, which is capped at 30."`
	CragsScanned int           `json:"cragsScanned" jsonschema:"How many crags were searched. Zero matches with a non-zero scan means the area has no routes fitting the filter, rather than nothing being searched."`
	SkippedNoYDS int           `json:"skippedNoYds,omitempty" jsonschema:"Trad routes excluded because no YDS grade is recorded for them."`
	Origin       ResolvedPlace `json:"origin"`
}

// core: no MCP types, returns a plain error
func findClimbs(ctx context.Context, gql graphql.Client, resolver geo.Resolver, args FindClimbsArgs) (FindClimbsResult, error) {
	origin, err := resolveOrigin(ctx, resolver, args.toCragsNearArgs())
	if err != nil {
		return FindClimbsResult{}, err
	}

	want, err := gradeRange(args.MinGrade, args.MaxGrade)
	if err != nil {
		return FindClimbsResult{}, err
	}

	areas, err := nearestCrags(ctx, gql, geo.Point{Lat: origin.Lat, Lng: origin.Lng}, args.MaxDistanceKm)
	if err != nil {
		return FindClimbsResult{}, err
	}

	details, err := fetchAreaDetails(ctx, gql, areas)
	if err != nil {
		return FindClimbsResult{}, err
	}

	point := geo.Point{Lat: origin.Lat, Lng: origin.Lng}
	out := make([]ClimbMatch, 0, MaxClimbs)
	skipped := 0
	scanned := 0
	for i, a := range areas {
		if details[i] == nil {
			continue
		}
		scanned++
		for _, c := range details[i].Climbs {
			// TODO: trad only. A discipline argument would replace this.
			if !c.Type.Trad {
				continue
			}
			if c.Grades.Yds == "" {
				skipped++
				continue
			}
			got, err := grade.ParseYDS(c.Grades.Yds)
			if err != nil {
				// A grade in some other system, or free text. Not a failure of
				// the search — count it with the ungraded and move on.
				skipped++
				continue
			}
			if !got.Overlaps(want) {
				continue
			}
			pitches := multipitch(c.Length)
			if args.MultipitchOnly && pitches == PitchesNo {
				continue
			}
			m := ClimbMatch{
				Name:       c.Name,
				UUID:       c.Uuid,
				Grade:      c.Grades.Yds,
				Multipitch: pitches,
				Crag:       a.AreaName,
				CragUUID:   a.Uuid,
				DistanceKm: distanceKm(point, a),
				Path:       a.PathTokens,
			}
			if c.Length > 0 {
				m.LengthM = c.Length
			}
			out = append(out, m)
		}
	}

	// Nearest crag first, then easiest route, so a reader working down the list
	// moves outward rather than jumping between crags.
	slices.SortStableFunc(out, func(a, b ClimbMatch) int {
		if d := cmp.Compare(a.DistanceKm, b.DistanceKm); d != 0 {
			return d
		}
		return cmp.Compare(gradeOrder(a.Grade), gradeOrder(b.Grade))
	})

	count := len(out)
	if len(out) > MaxClimbs {
		out = out[:MaxClimbs]
	}

	return FindClimbsResult{
		Climbs:       out,
		Count:        count,
		CragsScanned: scanned,
		SkippedNoYDS: skipped,
		Origin:       origin,
	}, nil
}

// nearestCrags runs the proximity search and returns the nearest crags, capped.
//
// The cap is the same MaxCrags crags_near uses, so a filtered search costs the
// API no more than an unfiltered one. It does mean a selective filter can miss
// a match sitting just outside the twenty nearest crags.
func nearestCrags(ctx context.Context, gql graphql.Client, origin geo.Point, maxDistanceKm *float64) ([]CragsNearCrag, error) {
	km := float64(defaultMaxDistanceKm)
	if maxDistanceKm != nil {
		km = *maxDistanceKm
	}
	if km <= 0 || km > maxDistanceLimitKm {
		return nil, fmt.Errorf("maxDistanceKm must be greater than 0 and at most %d, got %g", maxDistanceLimitKm, km)
	}

	// Upstream takes metres; the tools take kilometres. See crags_near.go.
	resp, err := generated.CragsNear(ctx, gql, generated.Point{Lat: origin.Lat, Lng: origin.Lng}, int(km*1000))
	if err != nil {
		return nil, err
	}

	var found []CragsNearCrag
	for _, group := range resp.CragsNear {
		found = append(found, group.Crags...)
	}
	slices.SortFunc(found, func(a, b CragsNearCrag) int {
		return cmp.Compare(distanceKm(origin, a), distanceKm(origin, b))
	})
	if len(found) > MaxCrags {
		found = found[:MaxCrags]
	}
	return found, nil
}

// gradeRange turns the requested bounds into one span. An omitted bound is
// open-ended rather than an error.
func gradeRange(minGrade, maxGrade string) (grade.Span, error) {
	span := grade.Span{Lo: 0, Hi: 1 << 30}
	if minGrade != "" {
		lo, err := grade.ParseYDS(minGrade)
		if err != nil {
			return span, fmt.Errorf("minGrade: %w (YDS only, for example 5.8 or 5.10b)", err)
		}
		span.Lo = lo.Lo
	}
	if maxGrade != "" {
		hi, err := grade.ParseYDS(maxGrade)
		if err != nil {
			return span, fmt.Errorf("maxGrade: %w (YDS only, for example 5.8 or 5.10b)", err)
		}
		span.Hi = hi.Hi
	}
	if span.Lo > span.Hi {
		return span, fmt.Errorf("minGrade %q is above maxGrade %q", minGrade, maxGrade)
	}
	return span, nil
}

// gradeOrder sorts by the low end of a grade's span, so an ambiguous "5.10"
// sits with 5.10a rather than floating.
//
// TODO: test sort stability if route ordering ever looks wrong.
func gradeOrder(s string) int {
	span, err := grade.ParseYDS(s)
	if err != nil {
		return 0
	}
	return span.Lo
}

// multipitch classifies a route from its recorded length. See
// multipitchMinLength for why this is inference rather than data.
func multipitch(length int) string {
	switch {
	case length <= 0:
		return PitchesUnknown
	case length >= multipitchMinLength:
		return PitchesYes
	default:
		return PitchesNo
	}
}

// toCragsNearArgs lets the two tools share resolveOrigin, which is the same
// question in both: place or coordinates, and did the caller give exactly one.
func (a FindClimbsArgs) toCragsNearArgs() CragsNearArgs {
	return CragsNearArgs{Place: a.Place, LngLat: a.LngLat, MaxDistanceKm: a.MaxDistanceKm}
}

// adapter: MCP signature, no logic
func HandleFindClimbs(gql graphql.Client, resolver geo.Resolver) mcp.ToolHandlerFor[FindClimbsArgs, FindClimbsResult] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args FindClimbsArgs) (*mcp.CallToolResult, FindClimbsResult, error) {
		out, err := findClimbs(ctx, gql, resolver, args)
		return nil, out, err
	}
}
