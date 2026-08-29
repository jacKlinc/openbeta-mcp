package tools

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
	"github.com/jacKlinc/openbeta-mcp/internal/grade"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// MaxClimbs caps the returned routes. Count reports how many matched.
const MaxClimbs = 30

// Inference, not data: Climb.pitches is empty on every climb upstream.
const multipitchMinLength = 60

// Three values, not a bool: length is often -1, and unknown != single pitch.
const (
	PitchesYes     = "yes"
	PitchesNo      = "no"
	PitchesUnknown = "unknown"
)

// The roped rock disciplines, and all that the disciplines argument accepts.
const (
	DisciplineSport   = "sport"
	DisciplineTrad    = "trad"
	DisciplineAlpine  = "alpine"
	DisciplineAid     = "aid"
	DisciplineTopRope = "tr"
)

// The rest are excluded because internal/grade parses neither vscale/font nor wi.
var ropedDisciplines = []string{DisciplineSport, DisciplineTrad, DisciplineAlpine, DisciplineAid, DisciplineTopRope}

// Named so a caller gets a reason rather than an unrecognised-name error.
var excludedDisciplines = map[string]string{
	"bouldering":    "boulder problems are graded in V-scale or Font, which the grade filter does not read",
	"boulder":       "boulder problems are graded in V-scale or Font, which the grade filter does not read",
	"deepwatersolo": "deep water solos are graded in Font, which the grade filter does not read",
	"ice":           "ice is graded in WI, a scale that cannot share a range with rock grades",
	"mixed":         "mixed routes are graded in WI, a scale that cannot share a range with rock grades",
	"snow":          "snow routes are graded in WI, a scale that cannot share a range with rock grades",
}

// FindClimbsArgs is the input schema for find_climbs.
type FindClimbsArgs struct {
	Place          string    `json:"place,omitempty" jsonschema:"A climbing destination or town, for example \"Squamish\". Supply this or lnglat, not both."`
	LngLat         []float64 `json:"lnglat,omitempty" jsonschema:"An explicit search origin as two numbers in the order [longitude, latitude]. Longitude comes first."`
	MaxDistanceKm  *float64  `json:"maxDistanceKm,omitempty" jsonschema:"Search radius in kilometres. Defaults to 20, maximum 500."`
	Disciplines    []string  `json:"disciplines,omitempty" jsonschema:"Which roped rock disciplines to return: any of \"sport\", \"trad\", \"alpine\", \"aid\", \"tr\" (top rope). Omit for all of them. Routes often carry more than one, so \"sport\" also returns routes that are both sport and top rope."`
	MinGrade       string    `json:"minGrade,omitempty" jsonschema:"Lowest grade to include, in the grading system of the crags being searched, for example \"5.8\" (YDS), \"6a+\" (French), \"7-\" (UIAA). Omit for no lower bound."`
	MaxGrade       string    `json:"maxGrade,omitempty" jsonschema:"Highest grade to include, in the same system as minGrade, for example \"5.10b\", \"7a\", \"8+\" or \"23\". Omit for no upper bound."`
	MultipitchOnly bool      `json:"multipitchOnly,omitempty" jsonschema:"Return only routes that are multi-pitch or whose length is unrecorded. The API stores no pitch count, so this is inferred from route length."`
}

// ClimbMatch is one route in a find_climbs result.
type ClimbMatch struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	// Meaningless apart, and one radius really can span two systems.
	Grade       string   `json:"grade" jsonschema:"The grade exactly as recorded upstream, which may be imprecise, for example \"5.10\" or \"5.11a/b\". Read it in the system named by gradeSystem."`
	GradeSystem string   `json:"gradeSystem" jsonschema:"Which system the grade is written in: \"yds\", \"french\" or \"uiaa\". Taken from the crag, so one result set may mix systems."`
	Disciplines []string `json:"disciplines" jsonschema:"The roped disciplines this route is recorded as. Routes commonly carry more than one."`
	Multipitch  string   `json:"multipitch" jsonschema:"\"yes\", \"no\", or \"unknown\" when the route's length is not recorded. Inferred from length, not from a pitch count."`
	LengthM     int      `json:"lengthM,omitempty" jsonschema:"Route length in metres. Absent when not recorded."`
	Crag        string   `json:"crag"`
	CragUUID    string   `json:"cragUuid"`
	DistanceKm  float64  `json:"distanceKm"`
	Path        []string `json:"path,omitempty"`
}

// FindClimbsResult is the output schema for find_climbs.
type FindClimbsResult struct {
	Climbs       []ClimbMatch  `json:"climbs" jsonschema:"Matching routes, nearest crag first. Capped at 30."`
	Count        int           `json:"count" jsonschema:"How many routes had matched when the search stopped. The search stops at 30, so this is a floor rather than a total."`
	CragsScanned int           `json:"cragsScanned" jsonschema:"How many crags were searched. Zero matches with a non-zero scan means the crags searched hold no routes fitting the filter, rather than nothing being searched."`
	Skipped      int           `json:"skipped,omitempty" jsonschema:"Routes excluded because their grade could not be read: none recorded in the crag's own system, free text, or bounds that cannot be written in that system."`
	Origin       ResolvedPlace `json:"origin"`
}

// core: no MCP types, returns a plain error
func findClimbs(ctx context.Context, gql graphql.Client, resolver geo.Resolver, args FindClimbsArgs) (FindClimbsResult, error) {
	origin, err := resolveOrigin(ctx, resolver, args.toCragsNearArgs())
	if err != nil {
		return FindClimbsResult{}, err
	}

	bounds, err := newGradeBounds(args.MinGrade, args.MaxGrade)
	if err != nil {
		return FindClimbsResult{}, err
	}

	wanted, err := wantedDisciplines(args.Disciplines)
	if err != nil {
		return FindClimbsResult{}, err
	}

	point := geo.Point{Lat: origin.Lat, Lng: origin.Lng}
	areas, _, err := nearestCrags(ctx, gql, point, args.MaxDistanceKm)
	if err != nil {
		return FindClimbsResult{}, err
	}

	found, err := scanCrags(ctx, gql, point, areas, bounds, wanted, args.MultipitchOnly)
	if err != nil {
		return FindClimbsResult{}, err
	}

	out := found.climbs
	// Nearest crag first, then easiest route, so the list reads outward.
	slices.SortStableFunc(out, func(a, b ClimbMatch) int {
		if d := cmp.Compare(a.DistanceKm, b.DistanceKm); d != 0 {
			return d
		}
		// Ordinals compare only within a system; leave cross-system ties alone.
		if a.GradeSystem != b.GradeSystem {
			return 0
		}
		return cmp.Compare(gradeOrder(a), gradeOrder(b))
	})

	count := len(out)
	if len(out) > MaxClimbs {
		out = out[:MaxClimbs]
	}

	return FindClimbsResult{
		Climbs:       out,
		Count:        count,
		CragsScanned: found.scanned,
		Skipped:      found.skipped,
		Origin:       origin,
	}, nil
}

// scanResult accumulates one search across however many crags it reaches.
type scanResult struct {
	climbs  []ClimbMatch
	scanned int
	skipped int
}

// Batched rather than fanned out so the early stop actually saves requests.
func scanCrags(
	ctx context.Context,
	gql graphql.Client,
	origin geo.Point,
	areas []CragsNearCrag,
	bounds gradeBounds,
	wanted []string,
	multipitchOnly bool,
) (scanResult, error) {
	out := scanResult{climbs: make([]ClimbMatch, 0, MaxClimbs)}

	attempted, failed := 0, 0
	var firstErr error

	for start := 0; start < len(areas) && len(out.climbs) < MaxClimbs; start += detailConcurrency {
		batch := areas[start:min(start+detailConcurrency, len(areas))]

		details, batchFailed, err := fetchAreaDetails(ctx, gql, batch)
		attempted += len(batch)
		failed += batchFailed
		if firstErr == nil {
			firstErr = err
		}

		// The batch is already paid for, so filtering it all tightens Count for free.
		for i, a := range batch {
			if details[i] == nil {
				continue
			}
			out.add(origin, a, details[i], bounds, wanted, multipitchOnly)
		}
	}

	// Judged across the whole search: five of five is bad luck at that size.
	if err := allFailed(attempted, failed, firstErr); err != nil {
		return scanResult{}, err
	}
	return out, nil
}

// add filters one crag's climbs into the result.
func (s *scanResult) add(
	origin geo.Point,
	area CragsNearCrag,
	detail *generated.GetAreaDetailsArea,
	bounds gradeBounds,
	wanted []string,
	multipitchOnly bool,
) {
	s.scanned++

	// gradeContext picks the parser, never the grade text: "7" is both French and UIAA.
	system, ok := grade.SystemFor(detail.GradeContext)
	if !ok {
		// British crags always land here: GradeType has no British field.
		s.skipped += len(detail.Climbs)
		return
	}

	// A YDS bound over a French crag: skipping beats guessing or ignoring the range.
	want, ok := bounds.spanIn(system)
	if !ok {
		s.skipped += len(detail.Climbs)
		return
	}

	for _, c := range detail.Climbs {
		got := disciplinesOf(c.Type)
		if !sharesAny(got, wanted) {
			continue
		}

		recorded := grade.Recorded(system, c.Grades.Yds, c.Grades.French, c.Grades.Uiaa)
		if recorded == "" {
			s.skipped++
			continue
		}
		span, err := grade.Parse(system, recorded)
		if err != nil {
			// Free text, or filed under the wrong system: count it and move on.
			s.skipped++
			continue
		}

		// Cannot disagree, both spans being built in system; skip if that changes.
		in, err := span.Overlaps(want)
		if err != nil {
			s.skipped++
			continue
		}
		if !in {
			continue
		}

		pitches := multipitch(c.Length)
		if multipitchOnly && pitches == PitchesNo {
			continue
		}

		m := ClimbMatch{
			Name:        c.Name,
			UUID:        c.Uuid,
			Grade:       recorded,
			GradeSystem: string(system),
			Disciplines: got,
			Multipitch:  pitches,
			Crag:        area.AreaName,
			CragUUID:    area.Uuid,
			DistanceKm:  distanceKm(origin, area),
			Path:        area.PathTokens,
		}
		if c.Length > 0 {
			m.LengthM = c.Length
		}
		s.climbs = append(s.climbs, m)
	}
}

// Returns the capped crags plus the uncapped total, which makes a near-miss visible.
func nearestCrags(ctx context.Context, gql graphql.Client, origin geo.Point, maxDistanceKm *float64) ([]CragsNearCrag, int, error) {
	km := float64(defaultMaxDistanceKm)
	if maxDistanceKm != nil {
		km = *maxDistanceKm
	}
	if km <= 0 || km > maxDistanceLimitKm {
		return nil, 0, fmt.Errorf("maxDistanceKm must be greater than 0 and at most %d, got %g", maxDistanceLimitKm, km)
	}

	// Upstream takes metres; the tools take kilometres. See crags_near.go.
	resp, err := generated.CragsNear(ctx, gql, generated.Point{Lat: origin.Lat, Lng: origin.Lng}, int(km*1000))
	if err != nil {
		return nil, 0, err
	}

	var found []CragsNearCrag
	for _, group := range resp.CragsNear {
		found = append(found, group.Crags...)
	}
	slices.SortFunc(found, func(a, b CragsNearCrag) int {
		return cmp.Compare(distanceKm(origin, a), distanceKm(origin, b))
	})

	total := len(found)
	if len(found) > MaxCrags {
		found = found[:MaxCrags]
	}
	return found, total, nil
}

// Held unparsed: bounds cannot be resolved until a crag names the system.
type gradeBounds struct{ min, max string }

// Rejects a non-grade up front; a valid grade the search cannot reach is not an error.
func newGradeBounds(minGrade, maxGrade string) (gradeBounds, error) {
	b := gradeBounds{min: strings.TrimSpace(minGrade), max: strings.TrimSpace(maxGrade)}

	shared := grade.Systems()
	for _, bound := range []struct{ name, value string }{{"minGrade", b.min}, {"maxGrade", b.max}} {
		if bound.value == "" {
			continue
		}
		in := grade.ParsesIn(bound.value)
		if len(in) == 0 {
			return b, fmt.Errorf("%s: %q is not a grade in any system this tool reads. Use one of %s",
				bound.name, bound.value, grade.Examples())
		}
		shared = intersectSystems(shared, in)
	}

	if b.min == "" || b.max == "" {
		return b, nil
	}

	// No crag satisfies bounds from two systems; say so rather than skip them all.
	if len(shared) == 0 {
		return b, fmt.Errorf("minGrade %q and maxGrade %q are in different grading systems; write both in the same one", b.min, b.max)
	}
	for _, system := range shared {
		lo, err := grade.Parse(system, b.min)
		if err != nil {
			continue
		}
		hi, err := grade.Parse(system, b.max)
		if err != nil {
			continue
		}
		if lo.Lo > hi.Hi {
			return b, fmt.Errorf("minGrade %q is above maxGrade %q", b.min, b.max)
		}
	}
	return b, nil
}

// False means unreadable in this crag's system, which is not the same as no match.
func (b gradeBounds) spanIn(system grade.System) (grade.Span, bool) {
	span := grade.Span{Lo: 0, Hi: math.MaxInt32, System: system}
	if b.min != "" {
		lo, err := grade.Parse(system, b.min)
		if err != nil {
			return span, false
		}
		span.Lo = lo.Lo
	}
	if b.max != "" {
		hi, err := grade.Parse(system, b.max)
		if err != nil {
			return span, false
		}
		span.Hi = hi.Hi
	}
	return span, true
}

func intersectSystems(a, b []grade.System) []grade.System {
	var out []grade.System
	for _, s := range a {
		if slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// Omitted means every roped discipline rather than none.
func wantedDisciplines(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return ropedDisciplines, nil
	}

	out := make([]string, 0, len(requested))
	for _, d := range requested {
		name := strings.ToLower(strings.TrimSpace(d))
		if slices.Contains(ropedDisciplines, name) {
			if !slices.Contains(out, name) {
				out = append(out, name)
			}
			continue
		}
		if why, excluded := excludedDisciplines[name]; excluded {
			return nil, fmt.Errorf("disciplines: %q is not returned by this tool — %s. Use crags_near to find the areas instead", d, why)
		}
		return nil, fmt.Errorf("disciplines: %q is not a discipline this tool knows. Use any of %s", d, strings.Join(ropedDisciplines, ", "))
	}
	return out, nil
}

// Ordered as ropedDisciplines lists them, so results read consistently.
func disciplinesOf(t generated.GetAreaDetailsAreaClimbsClimbType) []string {
	var out []string
	for _, d := range ropedDisciplines {
		if isDiscipline(t, d) {
			out = append(out, d)
		}
	}
	return out
}

func isDiscipline(t generated.GetAreaDetailsAreaClimbsClimbType, name string) bool {
	switch name {
	case DisciplineSport:
		return t.Sport
	case DisciplineTrad:
		return t.Trad
	case DisciplineAlpine:
		return t.Alpine
	case DisciplineAid:
		return t.Aid
	case DisciplineTopRope:
		return t.Tr
	default:
		return false
	}
}

// Any rather than all: a route is often both sport and top rope.
func sharesAny(got, wanted []string) bool {
	for _, g := range got {
		if slices.Contains(wanted, g) {
			return true
		}
	}
	return false
}

// Sorts on the low end, so an ambiguous "5.10" sits with 5.10a rather than floating.
func gradeOrder(m ClimbMatch) int {
	span, err := grade.Parse(grade.System(m.GradeSystem), m.Grade)
	if err != nil {
		return 0
	}
	return span.Lo
}

// Classified from length; see multipitchMinLength for why that is inference.
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

// Lets both tools share resolveOrigin, the same question in each.
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
