package openbeta

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live tests hit the real API. Opt in with OPENBETA_LIVE=1 so the default
// `go test ./...` stays offline and deterministic.
//
//	OPENBETA_LIVE=1 go test ./internal/openbeta -run Live -v
func liveClient(t *testing.T) (*Client, context.Context) {
	t.Helper()
	if os.Getenv("OPENBETA_LIVE") == "" {
		t.Skip("set OPENBETA_LIVE=1 to run tests against the live API")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return New(), ctx
}

// stawamusChief is a stable, well-populated area used as the verification
// vehicle throughout.
const stawamusChief = "8f267065-fc1a-59ce-bcf1-6e9335548363"

// squamishBBox is the box the schema findings were measured against.
var squamishBBox = BBox{-123.2, 49.6, -122.9, 49.8}

func TestLiveCragsWithin(t *testing.T) {
	c, ctx := liveClient(t)

	got, err := c.CragsWithin(ctx, squamishBBox, 13)
	if err != nil {
		t.Fatalf("CragsWithin: %v", err)
	}
	if len(got) < 100 {
		t.Errorf("expected >100 crags in Squamish at zoom 13, got %d", len(got))
	}
	for _, cr := range got {
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
	if got[0].ClimbCount < got[len(got)-1].ClimbCount {
		t.Error("results are not sorted by climb count")
	}
}

// The regression that motivates climbCount: these crags report totalClimbs 0
// upstream while holding real climbs, and must not be filtered out.
func TestLiveCragsWithinKeepsZeroTotalClimbsCrags(t *testing.T) {
	c, ctx := liveClient(t)

	got, err := c.CragsWithin(ctx, squamishBBox, 13)
	if err != nil {
		t.Fatalf("CragsWithin: %v", err)
	}
	byName := make(map[string]CragSummary, len(got))
	for _, cr := range got {
		byName[cr.Name] = cr
	}
	for _, name := range []string{"Tantalus Wall", "Neat and Cool", "Shannon Falls Wall"} {
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

// Low zoom returns parent areas, high zoom returns individual crags. Both must
// come back populated.
func TestLiveZoomThreshold(t *testing.T) {
	c, ctx := liveClient(t)

	parents, err := c.CragsWithin(ctx, squamishBBox, leafZoomThreshold-1)
	if err != nil {
		t.Fatalf("CragsWithin at low zoom: %v", err)
	}
	leaves, err := c.CragsWithin(ctx, squamishBBox, leafZoomThreshold)
	if err != nil {
		t.Fatalf("CragsWithin at leaf zoom: %v", err)
	}
	if len(parents) == 0 || len(leaves) == 0 {
		t.Fatalf("expected results at both zooms, got %d parents and %d leaves", len(parents), len(leaves))
	}
	if len(leaves) <= len(parents) {
		t.Errorf("expected more results at zoom %d (%d) than below it (%d)",
			leafZoomThreshold, len(leaves), len(parents))
	}
}
/* TODO: fix me
func TestLiveGetAreaParent(t *testing.T) {
	c, ctx := liveClient(t)

	got, err := c.GetArea(ctx, stawamusChief)
	if err != nil {
		t.Fatalf("GetArea: %v", err)
	}
	if got.Name != "Stawamus Chief" {
		t.Errorf("Name = %q, want Stawamus Chief", got.Name)
	}
	if got.Lat == 0 || got.Lng == 0 {
		t.Errorf("missing coordinates: (%g, %g)", got.Lat, got.Lng)
	}
	// The Chief holds no climbs directly; they live on its children.
	if len(got.Children) == 0 {
		t.Error("expected children for a parent area")
	}
	if len(got.Path) == 0 {
		t.Error("expected pathTokens")
	}
}


// Descending one level from a parent must reach climbs with names and grades.
func TestLiveGetAreaLeafHasClimbs(t *testing.T) {
	c, ctx := liveClient(t)

	parent, err := c.GetArea(ctx, stawamusChief)
	if err != nil {
		t.Fatalf("GetArea parent: %v", err)
	}

	var found bool
	for _, ch := range parent.Children {
		child, err := c.GetArea(ctx, ch.UUID)
		if err != nil {
			t.Fatalf("GetArea child %s: %v", ch.UUID, err)
		}
		if len(child.Climbs) == 0 {
			continue
		}
		found = true
		var graded int
		for _, cl := range child.Climbs {
			if cl.Name == "" {
				t.Errorf("climb %s has no name", cl.UUID)
			}
			if cl.Grade != "" {
				graded++
			}
		}
		if graded == 0 {
			t.Errorf("no climb in %q carries a grade", child.Name)
		}
		break
	}
	if !found {
		t.Error("no child of Stawamus Chief returned any climbs")
	}
}

func TestLiveGetAreaNotFound(t *testing.T) {
	c, ctx := liveClient(t)

	_, err := c.GetArea(ctx, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("expected an error for a nonexistent area")
	}
}
*/

// An ocean bbox is a legitimately empty answer, not a failure.
func TestLiveEmptyBBox(t *testing.T) {
	c, ctx := liveClient(t)

	got, err := c.CragsWithin(ctx, BBox{-140, -50, -139, -49}, 13)
	if err != nil {
		t.Fatalf("expected empty result, got error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no crags in the South Pacific, got %d", len(got))
	}
}
