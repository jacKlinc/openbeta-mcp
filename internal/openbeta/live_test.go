package openbeta_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
	"github.com/jacKlinc/openbeta-mcp/internal/tools"
)

// Live tests hit the real API. Opt in with OPENBETA_LIVE=1 so the default
// `go test ./...` stays offline and deterministic.
//
//	OPENBETA_LIVE=1 go test ./internal/openbeta -run Live -v
func liveClient(t *testing.T) (*openbeta.Client, context.Context, *graphql.Client) {
	t.Helper()
	if os.Getenv("OPENBETA_LIVE") == "" {
		t.Skip("set OPENBETA_LIVE=1 to run tests against the live API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	c := openbeta.New()
	gql := graphql.NewClient(c.Endpoint(), c.HTTPClient())

	return c, ctx, &gql
}

// stawamusChief is a stable, well-populated area used as the verification
// vehicle throughout.
const stawamusChief = "8f267065-fc1a-59ce-bcf1-6e9335548363"

// squamishBBox is the box the schema findings were measured against.
var squamishBBox = tools.BBox{-123.2, 49.6, -122.9, 49.8}

const leafZoomThreshold = 11

func callHandler(ctx context.Context, gql *graphql.Client, bbox tools.BBox) (any, tools.CragsWithinResult, error) {
	var zoom float64 = 13.0
	h := tools.HandleCragsWithin(gql)
	return h(ctx, nil, tools.CragsWithinArgs{BBox: bbox[:], Zoom: &zoom})
}

func TestLiveCragsWithin(t *testing.T) {
	_, ctx, gql := liveClient(t)

	_, got, err := callHandler(ctx, gql, tools.BBox(squamishBBox))
	if err != nil {
		t.Fatalf("CragsWithin: %v", err)
	}
	// Count is the number of crags in the box, not the number returned, so it
	// still measures the live result rather than the truncation.
	if got.Count < 100 {
		t.Errorf("expected >100 crags in Squamish at zoom 13, got %d", got.Count)
	}
	if len(got.Crags) != tools.MaxCrags {
		t.Errorf("expected a full page of %d crags, got %d", tools.MaxCrags, len(got.Crags))
	}
	for _, cr := range got.Crags {
		if cr.ClimbCount == 0 {
			t.Errorf("%q returned with zero climbs", cr.Name)
		}
		if cr.UUID == "" || cr.Name == "" {
			t.Errorf("crag missing identity: %+v", cr)
		}
		if cr.Lat < 49.5 || cr.Lat > 49.9 || cr.Lng < -123.3 || cr.Lng > -122.8 {
			t.Errorf("%q at (%g, %g) is outside the requested bbox", cr.Name, cr.Lat, cr.Lng)
		}
	}
	if got.Crags[0].ClimbCount < got.Crags[len(got.Crags)-1].ClimbCount {
		t.Error("results are not sorted by climb count")
	}
}

// The regression that motivates climbCount: these crags report totalClimbs 0
// upstream while holding real climbs, and must not be filtered out.
func TestLiveCragsWithinKeepsZeroTotalClimbsCrags(t *testing.T) {
	_, ctx, gql := liveClient(t)
	_, got, err := callHandler(ctx, gql, tools.BBox(squamishBBox))
	if err != nil {
		t.Fatalf("CragsWithin: %v", err)
	}
	byName := make(map[string]openbeta.CragSummary, len(got.Crags))
	for _, cr := range got.Crags {
		byName[cr.Name] = cr
	}
	for _, name := range []string{"The Apron", "Slhanay", "Parking Lot Wall"} {
		cr, ok := byName[name]
		if !ok {
			t.Errorf("%q missing from results (upstream totalClimbs is 0, but it has climbs)", name)
			continue
		}
		if cr.ClimbCount == 0 {
			t.Errorf("%q has ClimbCount 0", name)
		}
	}
}

// TODO: this needs to be changed to cragsNear to prevent overloading the API
// Low zoom returns parent areas, high zoom returns individual crags. Both must
// come back populated.
func TestLiveZoomThreshold(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleCragsWithin(gql)

	var zoom float64 = 10.0
	_, parents, err := h(ctx, nil, tools.CragsWithinArgs{BBox: squamishBBox[:], Zoom: &zoom})
	if err != nil {
		t.Fatalf("CragsWithin at low zoom: %v", err)
	}
	// Zoom in and run again
	zoom += 1
	_, leaves, err := h(ctx, nil, tools.CragsWithinArgs{BBox: squamishBBox[:], Zoom: &zoom})
	if err != nil {
		t.Fatalf("CragsWithin at leaf zoom: %v", err)
	}

	if parents.Count == 0 || leaves.Count == 0 {
		t.Fatalf("expected results at both zooms, got %d parents and %d leaves", parents.Count, leaves.Count)
	}
	if leaves.Count <= parents.Count {
		t.Errorf("expected more results at zoom %d (%d) than below it (%d)",
			leafZoomThreshold, leaves.Count, parents.Count)
	}
}

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
			if anyGrade(cl.Grades) != "" {
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

// anyGrade returns the first populated grade string, whichever system it is in.
// Which sibling is filled depends on the area's gradeContext and the climb type
// — YDS for Squamish trad, vscale for its boulders, the rest empty — so the test
// asks whether the climb is graded at all rather than picking one field.
func anyGrade(g generated.GetAreaDetailsAreaClimbsClimbGradesGradeType) string {
	for _, s := range []string{g.Yds, g.Vscale, g.Font, g.French, g.Uiaa, g.Ewbank, g.Wi} {
		if s != "" {
			return s
		}
	}
	return ""
}

func TestLiveGetAreaNotFound(t *testing.T) {
	_, ctx, gql := liveClient(t)
	h := tools.HandleGetAreaDetails(gql)

	_, _, err := h(ctx, nil, tools.GetAreaDetailsArgs{AreaID: "00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("expected an error for a nonexistent area")
	}
}

// An ocean bbox is a legitimately empty answer, not a failure.
func TestLiveEmptyBBox(t *testing.T) {
	_, ctx, gql := liveClient(t)
	_, got, err := callHandler(ctx, gql, tools.BBox{-140, -50, -139, -49})
	if err != nil {
		t.Fatalf("expected empty result, got error: %v", err)
	}
	if got.Count != 0 {
		t.Errorf("expected no crags in the South Pacific, got %d", got.Count)
	}
}
