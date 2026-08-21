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

// The roped rock disciplines: what find_climbs returns, and the whole of what
// the disciplines argument accepts.
const (
	DisciplineSport   = "sport"
	DisciplineTrad    = "trad"
	DisciplineAlpine  = "alpine"
	DisciplineAid     = "aid"
	DisciplineTopRope = "tr"
)

// ropedDisciplines is the default set, in the order matches are reported.
//
// Everything else is out of scope on purpose. Bouldering and deep water solo are
// graded in vscale and font; ice, mixed and snow in wi — and internal/grade
// parses none of those. Admitting any of them would advertise routes the grade
// filter then drops silently, which is the same broken promise the honest counts
// elsewhere in this package exist to remove. Ice belongs in a later pass with a
// grade axis of its own: a range spanning WI4 and 5.10 means nothing.
var ropedDisciplines = []string{DisciplineSport, DisciplineTrad, DisciplineAlpine, DisciplineAid, DisciplineTopRope}

// excludedDisciplines explains the ones a caller is most likely to ask for.
// Without this they would come back as an unrecognised name, which reads as a
// typo and invites a retry that fails the same way.
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
	// Grade and GradeSystem travel together and are meaningless apart: "6b" and
	// "5.10a" in one list with no marker cannot be told apart, and a search
	// radius really can span two systems.
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
	// Nearest crag first, then easiest route, so a reader working down the list
	// moves outward rather than jumping between crags.
	slices.SortStableFunc(out, func(a, b ClimbMatch) int {
		if d := cmp.Compare(a.DistanceKm, b.DistanceKm); d != 0 {
			return d
		}
		// Ordinals only mean anything within a system. A distance tie is
		// normally two routes on one crag, so this rarely bites; where it does,
		// leaving the order alone beats inventing one across scales.
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

// scanCrags walks the crags nearest first, fetching their climbs a batch at a
// time and stopping once MaxClimbs routes have matched.
//
// Batched rather than fanned out across the whole list, because the stop only
// pays if it reaches upstream. The cap sweep measured MaxCrags 20→40 leaving the
// find_climbs token p95 flat at 3,761 while requests rose 484→772: every crag
// past the point of saturation was fetched and filtered purely to be discarded.
// Nearest-first ordering means the routes kept are the same ones either way.
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

		// Every crag in the batch is filtered even once the cap is reached: the
		// requests are already paid for, and the extra matches make Count a
		// tighter floor at no cost.
		for i, a := range batch {
			if details[i] == nil {
				continue
			}
			out.add(origin, a, details[i], bounds, wanted, multipitchOnly)
		}
	}

	// Judged across the whole search rather than per batch: five failures out of
	// five is bad luck at that size, and an outage only if nothing else worked.
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

	// The crag's own gradeContext picks the parser, never the grade string: "7"
	// is a French, UIAA grade at once, so reading a system off the
	// text would be a guess wearing a filter's clothes.
	system, ok := grade.SystemFor(detail.GradeContext)
	if !ok {
		// British crags land here and always will — OpenBeta's GradeType has no
		// British field, so there is no E-grade recorded for anything to read.
		s.skipped += len(detail.Climbs)
		return
	}

	// A YDS bound over a French crag. Skipping is the honest answer; converting
	// would turn the filter into a guess, and returning the routes unfiltered
	// would ignore the range the caller asked for.
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
			// Free text, or a grade filed under the wrong system. Not a failure
			// of the search — count it with the ungraded and move on.
			s.skipped++
			continue
		}

		// Both spans were built in system, so this cannot disagree. Checked
		// anyway: a skip is the right failure if that ever stops being true.
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

// nearestCrags runs the proximity search and returns the nearest crags capped at
// MaxCrags, along with how many the radius found before the cap.
//
// The cap is the same MaxCrags crags_near uses, so a filtered search costs the
// API no more than an unfiltered one. It does mean a selective filter can miss
// a match sitting just outside the twenty nearest crags — which is what the
// uncapped count exists to make visible.
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

// gradeBounds is the requested range as the caller wrote it.
//
// Held unparsed because a grade string does not name its own system and a crag
// does: the same "7" is one grade at Fontainebleau and another at Arapiles, so
// the bounds cannot be resolved until an area says which scale to read them in.
type gradeBounds struct{ min, max string }

// newGradeBounds checks the bounds are grades at all, before any request is made.
//
// That is the only check available up front, and it is the one worth having:
// "V4" is a mistake the caller must fix, where "6a" against a Yosemite search is
// a perfectly good grade the search simply will not reach. Rejecting the first
// and skipping the second is the difference between an error a model can act on
// and one it cannot.
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

	// Two bounds from different systems cannot describe one range, and no crag
	// would ever satisfy both. Better said here than silently skipping every
	// crag in the radius.
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

// spanIn resolves the bounds in one crag's system, reporting false when they
// cannot be read there.
//
// False does not mean "nothing matched": it means the question was asked in a
// scale this crag does not use, which is a different answer and is counted
// separately.
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

// wantedDisciplines turns the argument into the set to filter on. Omitted means
// every roped discipline rather than none.
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

// disciplinesOf reports which roped disciplines a route is recorded as, in the
// order ropedDisciplines lists them so results read consistently.
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

// sharesAny reports whether a route's disciplines include any that were asked
// for.
//
// Any rather than all: a route carries several booleans at once — upstream notes
// one may be both sport and top rope — so requiring all of them would return
// almost nothing.
func sharesAny(got, wanted []string) bool {
	for _, g := range got {
		if slices.Contains(wanted, g) {
			return true
		}
	}
	return false
}

// gradeOrder sorts by the low end of a grade's span, so an ambiguous "5.10"
// sits with 5.10a rather than floating.
//
// Read in the match's own system: the ordinals of two systems are unrelated
// numbers, which is why the caller only compares matches that share one.
func gradeOrder(m ClimbMatch) int {
	span, err := grade.Parse(grade.System(m.GradeSystem), m.Grade)
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
