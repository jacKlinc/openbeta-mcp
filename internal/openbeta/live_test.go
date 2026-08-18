package openbeta_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
	"github.com/jacKlinc/openbeta-mcp/internal/grade"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/tools"
)

// Live tests hit a real API. Opt in with OPENBETA_LIVE=1 so the default
// `go test ./...` stays offline and deterministic.
//
//	OPENBETA_LIVE=1 go test ./internal/openbeta -run Live -v
//
// OPENBETA_ENDPOINT redirects them at a local stack instead of the public API,
// which is a free service run by volunteers:
//
//	OPENBETA_LIVE=1 OPENBETA_ENDPOINT=http://localhost:4000 go test ./internal/openbeta -run Live -v
func liveClient(t *testing.T) (*openbeta.Client, context.Context, graphql.Client) {
	t.Helper()
	if os.Getenv("OPENBETA_LIVE") == "" {
		t.Skip("set OPENBETA_LIVE=1 to run tests against the live API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	// Without this the tests silently talk to production no matter what the
	// environment says, which makes a local run a lie.
	var opts []openbeta.Option
	if ep := os.Getenv("OPENBETA_ENDPOINT"); ep != "" {
		opts = append(opts, openbeta.WithEndpoint(ep))
	}
	c := openbeta.New(opts...)
	t.Logf("live endpoint: %s", c.Endpoint())
	gql := graphql.NewClient(c.Endpoint(), c.HTTPClient())

	return c, ctx, gql
}

// stawamusChief is a stable, well-populated area used as the verification
// vehicle throughout.
const stawamusChief = "8f267065-fc1a-59ce-bcf1-6e9335548363"

func TestLiveGetAreaParent(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleGetAreaDetails(gql)

	_, got, err := h(ctx, nil, tools.GetAreaDetailsArgs{AreaID: stawamusChief})
	if err != nil {
		t.Fatalf("GetArea: %v", err)
	}
	if got.Area.AreaName != "Stawamus Chief" {
		t.Errorf("Name = %q, want Stawamus Chief", got.Area.AreaName)
	}
	if got.Area.Metadata.Lat == 0 || got.Area.Metadata.Lng == 0 {
		t.Errorf("missing coordinates: (%g, %g)", got.Area.Metadata.Lat, got.Area.Metadata.Lng)
	}
	// The Chief holds no climbs directly; they live on its children.
	if len(got.Area.Children) == 0 {
		t.Error("expected children for a parent area")
	}
	if len(got.Area.PathTokens) == 0 {
		t.Error("expected pathTokens")
	}
}

// Descending one level from a parent must reach climbs with names and grades.
func TestLiveGetAreaLeafHasClimbs(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleGetAreaDetails(gql)

	_, parent, err := h(ctx, nil, tools.GetAreaDetailsArgs{AreaID: stawamusChief})
	if err != nil {
		t.Fatalf("GetArea parent: %v", err)
	}

	var found bool
	for _, ch := range parent.Area.Children {
		_, child, err := h(ctx, nil, tools.GetAreaDetailsArgs{AreaID: ch.Uuid})
		if err != nil {
			t.Fatalf("GetArea child %s: %v", ch.Uuid, err)
		}
		if len(child.Area.Climbs) == 0 {
			continue
		}
		found = true
		var graded int
		for _, cl := range child.Area.Climbs {
			if cl.Name == "" {
				t.Errorf("climb %s has no name", cl.Uuid)
			}
			if tools.PreferredGrade(cl.Grades, child.Area.Metadata.IsBoulder) != "" {
				graded++
			}
		}
		// Individual ungraded climbs are ordinary — a whole crag of them is not,
		// and means the grades selection stopped decoding.
		if graded == 0 {
			t.Errorf("no climb in %q carries a grade in any system", child.Area.AreaName)
		}
		break
	}
	if !found {
		t.Error("no child of Stawamus Chief returned any climbs")
	}
}

func TestLiveGetAreaNotFound(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleGetAreaDetails(gql)

	_, _, err := h(ctx, nil, tools.GetAreaDetailsArgs{AreaID: "00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent area")
	}
}

// crags_near end to end: resolve a place, search by radius, fan out for climb
// counts.
//
// The climb counts are the assertion that matters. cragsNear itself returns no
// climbs at all (docs/graphql-findings.md §4), so a non-zero count can only come
// from the fan-out. It also pins the unit conversion: maxDistance is metres
// upstream, so a 5 km search that forgot to multiply would ask for 5 metres and
// return nothing.
func TestLiveCragsNear(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleCragsNear(gql, geo.NewGazetteer())

	radiusKm := 5.0
	_, got, err := h(ctx, nil, tools.CragsNearArgs{Place: "Squamish", MaxDistanceKm: &radiusKm})
	if err != nil {
		t.Fatalf("CragsNear: %v", err)
	}
	if len(got.Crags) == 0 {
		t.Fatal("no crags within 5km of Squamish; maxDistance is metres upstream, check the conversion")
	}
	if got.Origin.Source != "gazetteer" {
		t.Errorf("Origin.Source = %q, want gazetteer", got.Origin.Source)
	}
	for _, cr := range got.Crags {
		if cr.ClimbCount == 0 {
			t.Errorf("%q returned with zero climbs", cr.Name)
		}
		if cr.UUID == "" || cr.Name == "" {
			t.Errorf("crag missing identity: %+v", cr)
		}
		if cr.DistanceKm > radiusKm {
			t.Errorf("%q at %.2f km is outside the %g km radius", cr.Name, cr.DistanceKm, radiusKm)
		}
	}
	if got.Crags[0].ClimbCount < got.Crags[len(got.Crags)-1].ClimbCount {
		t.Error("results are not sorted by climb count")
	}
}

// An ocean coordinate is a legitimately empty answer, not a failure.
func TestLiveCragsNearEmpty(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleCragsNear(gql, geo.NewGazetteer())

	_, got, err := h(ctx, nil, tools.CragsNearArgs{LngLat: []float64{-139, -49}})
	if err != nil {
		t.Fatalf("expected empty result, got error: %v", err)
	}
	if got.Count != 0 {
		t.Errorf("expected no crags in the South Pacific, got %d", got.Count)
	}
}

// find_climbs end to end against Squamish, which has known matches: Granville
// Street 5.8 at 115m, Sparrow 5.9 at 182m, Long Time No See 5.9 at 250m.
//
// Squamish rather than Whistler deliberately — Whistler holds no multi-pitch
// trad at all in OpenBeta, so it would make a test that passes on an empty
// result.
func TestLiveFindClimbs(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleFindClimbs(gql, geo.NewGazetteer())

	km := 10.0
	_, got, err := h(ctx, nil, tools.FindClimbsArgs{
		Place: "Squamish", MaxDistanceKm: &km,
		MinGrade: "5.8", MaxGrade: "5.10b", MultipitchOnly: true,
	})
	if err != nil {
		t.Fatalf("FindClimbs: %v", err)
	}
	if got.CragsScanned == 0 {
		t.Fatal("no crags scanned, so the filter proved nothing")
	}
	if len(got.Climbs) == 0 {
		t.Fatal("expected multi-pitch trad in Squamish between 5.8 and 5.10b")
	}

	var sawKnownMultipitch bool
	for _, c := range got.Climbs {
		if c.Multipitch == tools.PitchesNo {
			t.Errorf("%q is single pitch and should not be in a multipitchOnly result", c.Name)
		}
		if c.Multipitch == tools.PitchesYes {
			sawKnownMultipitch = true
		}
		// Every result must be inside the requested range. Parsing here rather
		// than trusting the tool is the point of the assertion.
		span, err := grade.ParseYDS(c.Grade)
		if err != nil {
			t.Errorf("%q has an unparseable grade %q", c.Name, c.Grade)
			continue
		}
		lo, _ := grade.ParseYDS("5.8")
		hi, _ := grade.ParseYDS("5.10b")
		if !span.Overlaps(grade.Span{Lo: lo.Lo, Hi: hi.Hi}) {
			t.Errorf("%q at %s is outside 5.8-5.10b", c.Name, c.Grade)
		}
	}
	if !sawKnownMultipitch {
		t.Error("expected at least one route with a recorded multi-pitch length")
	}
}
