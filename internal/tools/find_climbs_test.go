package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
)

// climbJSON builds one climb for a stubbed getAreaDetails response.
func climbJSON(name, yds string, length int, trad bool) string {
	discipline := `"sport":true`
	if trad {
		discipline = `"trad":true`
	}
	return `{"uuid":"` + name + `","name":"` + name + `","fa":"","length":` + itoa2(length) +
		`,"safety":"UNSPECIFIED","type":{` + discipline + `},"grades":{"yds":"` + yds + `"}}`
}

func itoa2(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	return itoa(n)
}

func areaWithClimbs(uuid string, climbs ...string) string {
	return `{"data":{"area":{"uuid":"` + uuid + `","areaName":"Crag ` + uuid + `","totalClimbs":0,` +
		`"metadata":{"lat":49.70,"lng":-123.15},"climbs":[` + strings.Join(climbs, ",") + `],"children":[]}}}`
}

// The filter has to hold on all three axes at once: discipline, grade range and
// pitches. Each row below is a climb the search should or should not return.
func TestFindClimbsFilters(t *testing.T) {
	climbs := []string{
		climbJSON("Trad In Range Long", "5.9", 250, true),   // keep
		climbJSON("Trad In Range Short", "5.10a", 20, true), // keep unless multipitchOnly
		climbJSON("Trad Unknown Length", "5.8", -1, true),   // keep, pitches unknown
		climbJSON("Trad Ambiguous Grade", "5.10", 91, true), // keep, spans into range
		climbJSON("Trad Too Hard", "5.11a", 120, true),      // drop, above range
		climbJSON("Trad Too Easy", "5.7", 120, true),        // drop, below range
		climbJSON("Sport In Range", "5.9", 120, false),      // drop, not trad
		climbJSON("Trad No Grade", "", 120, true),           // drop, counted as skipped
	}
	gql, _ := routingStub(t, nearBody("a"), func(string) (string, bool) {
		return areaWithClimbs("a", climbs...), false
	})

	t.Run("grade range and discipline", func(t *testing.T) {
		_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.8", MaxGrade: "5.10b"})
		if err != nil {
			t.Fatalf("find_climbs: %v", err)
		}

		got := map[string]ClimbMatch{}
		for _, c := range out.Climbs {
			got[c.Name] = c
		}
		want := []string{"Trad In Range Long", "Trad In Range Short", "Trad Unknown Length", "Trad Ambiguous Grade"}
		if len(got) != len(want) {
			t.Fatalf("expected %d matches, got %d: %+v", len(want), len(got), out.Climbs)
		}
		for _, name := range want {
			if _, ok := got[name]; !ok {
				t.Errorf("%q missing from results", name)
			}
		}
		// -1 must not be reported as single pitch; that is the distinction the
		// three-valued field exists for.
		if got["Trad Unknown Length"].Multipitch != PitchesUnknown {
			t.Errorf("unrecorded length = %q, want %q", got["Trad Unknown Length"].Multipitch, PitchesUnknown)
		}
		if got["Trad In Range Long"].Multipitch != PitchesYes {
			t.Errorf("250m route = %q, want %q", got["Trad In Range Long"].Multipitch, PitchesYes)
		}
		if got["Trad In Range Short"].Multipitch != PitchesNo {
			t.Errorf("20m route = %q, want %q", got["Trad In Range Short"].Multipitch, PitchesNo)
		}
		if out.SkippedNoYDS != 1 {
			t.Errorf("SkippedNoYDS = %d, want 1 (the ungraded trad route)", out.SkippedNoYDS)
		}
		if out.CragsScanned != 1 {
			t.Errorf("CragsScanned = %d, want 1", out.CragsScanned)
		}
	})

	t.Run("multipitchOnly keeps unknown lengths", func(t *testing.T) {
		_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.8", MaxGrade: "5.10b", MultipitchOnly: true})
		if err != nil {
			t.Fatalf("find_climbs: %v", err)
		}
		for _, c := range out.Climbs {
			if c.Multipitch == PitchesNo {
				t.Errorf("%q is single pitch and should have been filtered out", c.Name)
			}
			if c.Name == "Trad In Range Short" {
				t.Error("the 20m route should have been filtered out")
			}
		}
		var sawUnknown bool
		for _, c := range out.Climbs {
			if c.Name == "Trad Unknown Length" {
				sawUnknown = true
			}
		}
		if !sawUnknown {
			t.Error("a route with no recorded length must survive multipitchOnly, flagged unknown")
		}
	})
}

// A bad grade must be rejected with a message naming the system, since the model
// will otherwise retry with the same V-scale or French grade.
func TestFindClimbsRejectsNonYDSBounds(t *testing.T) {
	gql, called := routingStub(t, nearBody("a"), func(string) (string, bool) {
		return areaWithClimbs("a"), false
	})

	_, _, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
		FindClimbsArgs{Place: "Squamish", MinGrade: "V4"})
	if err == nil {
		t.Fatal("expected an error for a non-YDS bound")
	}
	if !strings.Contains(err.Error(), "YDS") {
		t.Errorf("error %q should name the grade system", err)
	}
	if called.Load() {
		t.Error("upstream was called despite an invalid grade")
	}
}
