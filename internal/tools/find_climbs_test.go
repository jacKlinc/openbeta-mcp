package tools

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
)

// climbJSON builds one climb for a stubbed getAreaDetails response.
//
// grades is the recorded-grade object rather than a YDS string, so a test can
// put a French grade on a French crag — which is the case the area-driven parser
// selection exists for.
func climbJSON(name, grades string, length int, disciplines ...string) string {
	types := make([]string, 0, len(disciplines))
	for _, d := range disciplines {
		types = append(types, `"`+d+`":true`)
	}
	return `{"uuid":"` + name + `","name":"` + name + `","fa":"","length":` + itoa2(length) +
		`,"safety":"UNSPECIFIED","type":{` + strings.Join(types, ",") + `},"grades":{` + grades + `}}`
}

func ydsGrade(g string) string    { return `"yds":"` + g + `"` }
func frenchGrade(g string) string { return `"french":"` + g + `"` }

func itoa2(n int) string {
	if n < 0 {
		return "-" + itoa(-n)
	}
	return itoa(n)
}

// areaWithClimbs stubs one crag. gradeContext is not optional here for the same
// reason it is not optional upstream: it is what decides which system the
// crag's grades are read in.
func areaWithClimbs(uuid, gradeContext string, climbs ...string) string {
	return `{"data":{"area":{"uuid":"` + uuid + `","areaName":"Crag ` + uuid + `","totalClimbs":0,` +
		`"gradeContext":"` + gradeContext + `",` +
		`"metadata":{"lat":49.70,"lng":-123.15},"climbs":[` + strings.Join(climbs, ",") + `],"children":[]}}}`
}

func names(climbs []ClimbMatch) []string {
	out := make([]string, 0, len(climbs))
	for _, c := range climbs {
		out = append(out, c.Name)
	}
	slices.Sort(out)
	return out
}

// The filter has to hold on all three axes at once: discipline, grade range and
// pitches. Each row below is a climb the search should or should not return.
func TestFindClimbsFilters(t *testing.T) {
	climbs := []string{
		climbJSON("Trad In Range Long", ydsGrade("5.9"), 250, "trad"),   // keep
		climbJSON("Trad In Range Short", ydsGrade("5.10a"), 20, "trad"), // keep unless multipitchOnly
		climbJSON("Trad Unknown Length", ydsGrade("5.8"), -1, "trad"),   // keep, pitches unknown
		climbJSON("Trad Ambiguous Grade", ydsGrade("5.10"), 91, "trad"), // keep, spans into range
		climbJSON("Trad Too Hard", ydsGrade("5.11a"), 120, "trad"),      // drop, above range
		climbJSON("Trad Too Easy", ydsGrade("5.7"), 120, "trad"),        // drop, below range
		// Kept now that the tool is not trad-only. It was dropped before, which
		// is the behaviour change the token baselines have to be re-measured for.
		climbJSON("Sport In Range", ydsGrade("5.9"), 120, "sport"),
		// One route, two disciplines: the filter is any match, not all.
		climbJSON("Sport And Top Rope", ydsGrade("5.9"), 30, "sport", "tr"),
		// Roped rock only. A boulder problem is out of scope whatever its grade
		// field says, because the scale it is really graded on is not parsed.
		climbJSON("Boulder In Range", ydsGrade("5.9"), -1, "bouldering"),
		climbJSON("Trad No Grade", ydsGrade(""), 120, "trad"), // drop, counted as skipped
	}
	gql, _ := routingStub(t, nearBody("a"), func(string) (string, bool) {
		return areaWithClimbs("a", "US", climbs...), false
	})

	t.Run("grade range, discipline and pitches", func(t *testing.T) {
		_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.8", MaxGrade: "5.10b"})
		if err != nil {
			t.Fatalf("find_climbs: %v", err)
		}

		got := map[string]ClimbMatch{}
		for _, c := range out.Climbs {
			got[c.Name] = c
		}
		want := []string{
			"Trad In Range Long", "Trad In Range Short", "Trad Unknown Length",
			"Trad Ambiguous Grade", "Sport In Range", "Sport And Top Rope",
		}
		if len(got) != len(want) {
			t.Fatalf("expected %d matches, got %d: %v", len(want), len(got), names(out.Climbs))
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
		// A grade with no system beside it cannot be read: "6b" and "5.10a" in
		// one list are indistinguishable without it.
		for _, c := range out.Climbs {
			if c.GradeSystem != "yds" {
				t.Errorf("%q reports gradeSystem %q, want yds", c.Name, c.GradeSystem)
			}
		}
		if got := got["Sport And Top Rope"].Disciplines; !slices.Equal(got, []string{"sport", "tr"}) {
			t.Errorf("Disciplines = %v, want [sport tr]", got)
		}
		if got := got["Trad In Range Long"].Disciplines; !slices.Equal(got, []string{"trad"}) {
			t.Errorf("Disciplines = %v, want [trad]", got)
		}
		if out.Skipped != 1 {
			t.Errorf("Skipped = %d, want 1 (the ungraded trad route)", out.Skipped)
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

// The discipline argument is what lets the corpus vary discipline and MLflow
// compare across it, so each of these is a series the eval harness can produce.
func TestFindClimbsDisciplineArgument(t *testing.T) {
	climbs := []string{
		climbJSON("Trad Only", ydsGrade("5.9"), 40, "trad"),
		climbJSON("Sport Only", ydsGrade("5.9"), 40, "sport"),
		climbJSON("Sport And Top Rope", ydsGrade("5.9"), 40, "sport", "tr"),
		climbJSON("Trad And Aid", ydsGrade("5.9"), 40, "trad", "aid"),
		climbJSON("Alpine", ydsGrade("5.9"), 40, "alpine"),
		climbJSON("Boulder", ydsGrade("5.9"), 40, "bouldering"),
		climbJSON("Ice", ydsGrade("5.9"), 40, "ice"),
	}
	gql, _ := routingStub(t, nearBody("a"), func(string) (string, bool) {
		return areaWithClimbs("a", "US", climbs...), false
	})

	tests := []struct {
		name        string
		disciplines []string
		want        []string
	}{
		{
			// Omitted is every roped discipline, not none. Bouldering and ice
			// stay out whatever is asked for.
			"omitted means every roped discipline", nil,
			[]string{"Alpine", "Sport And Top Rope", "Sport Only", "Trad And Aid", "Trad Only"},
		},
		{"trad reproduces the old behaviour", []string{"trad"}, []string{"Trad And Aid", "Trad Only"}},
		{"sport", []string{"sport"}, []string{"Sport And Top Rope", "Sport Only"}},
		// Any match, not all: a route that is both sport and top rope answers
		// to either, so the two sets overlap without being equal.
		{"top rope", []string{"tr"}, []string{"Sport And Top Rope"}},
		{"aid", []string{"aid"}, []string{"Trad And Aid"}},
		{"several at once", []string{"trad", "alpine"}, []string{"Alpine", "Trad And Aid", "Trad Only"}},
		{"case and spacing are forgiven", []string{" Trad "}, []string{"Trad And Aid", "Trad Only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
				FindClimbsArgs{Place: "Squamish", Disciplines: tt.disciplines})
			if err != nil {
				t.Fatalf("find_climbs: %v", err)
			}
			if got := names(out.Climbs); !slices.Equal(got, tt.want) {
				t.Errorf("disciplines %v returned %v, want %v", tt.disciplines, got, tt.want)
			}
		})
	}
}

// A bad argument must be rejected before the network call, with a message that
// says what would have worked — otherwise the model retries the same way.
func TestFindClimbsRejectsBadArgumentsBeforeCallingUpstream(t *testing.T) {
	tests := []struct {
		name string
		args FindClimbsArgs
		want string
	}{
		{
			// V-scale is not a system this tool reads, so it is a mistake to
			// fix rather than a search that found nothing.
			"grade in no system",
			FindClimbsArgs{Place: "Squamish", MinGrade: "V4"},
			"not a grade in any system",
		},
		{
			// Two systems cannot describe one range, and no crag could satisfy
			// both — better said than silently skipped everywhere.
			"bounds in different systems",
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.8", MaxGrade: "6a"},
			"different grading systems",
		},
		{
			"inverted bounds",
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.10b", MaxGrade: "5.8"},
			"is above",
		},
		{
			// Naming the scale is the point: "unknown discipline" would read as
			// a typo and invite the same retry.
			"bouldering explains itself",
			FindClimbsArgs{Place: "Squamish", Disciplines: []string{"bouldering"}},
			"V-scale or Font",
		},
		{
			"ice explains itself",
			FindClimbsArgs{Place: "Squamish", Disciplines: []string{"ice"}},
			"WI",
		},
		{
			"an actual typo",
			FindClimbsArgs{Place: "Squamish", Disciplines: []string{"tradd"}},
			"not a discipline",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql, called := routingStub(t, nearBody("a"), func(string) (string, bool) {
				return areaWithClimbs("a", "US"), false
			})

			_, _, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil, tt.args)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not explain the problem (want %q)", err, tt.want)
			}
			if called.Load() {
				t.Error("upstream was called despite an invalid argument")
			}
		})
	}
}

// One search radius can span two grade systems — Fontainebleau returns both FR
// and US across its subtree — so the parser is picked per crag, and a crag the
// bounds cannot be read at is skipped rather than converted.
func TestFindClimbsMixedGradeContexts(t *testing.T) {
	contexts := map[string]string{
		"us": areaWithClimbs("us", "US",
			climbJSON("Yosemite Route", ydsGrade("5.10a"), 40, "sport")),
		"fr": areaWithClimbs("fr", "FR",
			climbJSON("Siurana Route", frenchGrade("6a"), 40, "sport")),
		// OpenBeta's GradeType has no British field, so a UK crag carries no
		// grade any parser here could read.
		"uk": areaWithClimbs("uk", "UK",
			climbJSON("Stanage Route", ydsGrade(""), 40, "trad")),
	}
	gql, _ := routingStub(t, nearBody("us", "fr", "uk"), func(uuid string) (string, bool) {
		return contexts[uuid], false
	})

	tests := []struct {
		name       string
		args       FindClimbsArgs
		want       []string
		wantSystem string
	}{
		{
			// A YDS bound reaches the US crag only. The French route is not a
			// non-match, it is a route the question cannot be asked about.
			"yds bounds reach the us crag",
			FindClimbsArgs{Place: "Squamish", MinGrade: "5.9", MaxGrade: "5.11a"},
			[]string{"Yosemite Route"}, "yds",
		},
		{
			"french bounds reach the french crag",
			FindClimbsArgs{Place: "Squamish", MinGrade: "5c", MaxGrade: "6b"},
			[]string{"Siurana Route"}, "french",
		},
		{
			// With no bounds every crag whose system is known is readable, so
			// both come back, each quoting its own scale.
			"no bounds reach both",
			FindClimbsArgs{Place: "Squamish"},
			[]string{"Siurana Route", "Yosemite Route"}, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil, tt.args)
			if err != nil {
				t.Fatalf("find_climbs: %v", err)
			}
			if got := names(out.Climbs); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.wantSystem != "" {
				for _, c := range out.Climbs {
					if c.GradeSystem != tt.wantSystem {
						t.Errorf("%q reports gradeSystem %q, want %q", c.Name, c.GradeSystem, tt.wantSystem)
					}
				}
			}
			// All three crags were looked at, and the ones that could not be
			// read are counted rather than quietly vanishing.
			if out.CragsScanned != 3 {
				t.Errorf("CragsScanned = %d, want 3", out.CragsScanned)
			}
			if out.Skipped == 0 {
				t.Error("routes that could not be read must be counted in Skipped")
			}
		})
	}

	// The British case is worth pinning on its own, because it is permanent:
	// no bound in any system will ever reach a UK crag.
	t.Run("a uk crag is never reachable", func(t *testing.T) {
		_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish"})
		if err != nil {
			t.Fatalf("find_climbs: %v", err)
		}
		for _, c := range out.Climbs {
			if c.Crag == "Crag uk" {
				t.Errorf("%q came from a crag with no representable grade system", c.Name)
			}
		}
	})
}

// The cap has to stop the search, not just trim its output: the crags past it
// were being fetched and filtered purely to be discarded, at 60% more upstream
// requests for nothing.
func TestFindClimbsStopsFetchingOnceCapped(t *testing.T) {
	var uuids []string
	for i := 0; i < MaxCrags; i++ {
		uuids = append(uuids, "c"+itoa(i))
	}

	// Every crag holds more than the cap on its own, so the very first batch
	// settles the answer.
	var climbs []string
	for i := 0; i < MaxClimbs+5; i++ {
		climbs = append(climbs, climbJSON("Route "+itoa(i), ydsGrade("5.9"), 40, "trad"))
	}

	var fetched atomic.Int32
	gql, _ := routingStub(t, nearBody(uuids...), func(uuid string) (string, bool) {
		fetched.Add(1)
		return areaWithClimbs(uuid, "US", climbs...), false
	})

	_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
		FindClimbsArgs{Place: "Squamish"})
	if err != nil {
		t.Fatalf("find_climbs: %v", err)
	}

	if len(out.Climbs) != MaxClimbs {
		t.Errorf("returned %d climbs, want the cap of %d", len(out.Climbs), MaxClimbs)
	}
	// One batch of detailConcurrency, and not one crag more.
	if got := int(fetched.Load()); got != detailConcurrency {
		t.Errorf("fetched %d crags, want %d — the search did not stop at the cap", got, detailConcurrency)
	}
	if out.CragsScanned != detailConcurrency {
		t.Errorf("CragsScanned = %d, want %d", out.CragsScanned, detailConcurrency)
	}
	// Count is a floor now, and has to be at least the cap for that to mean
	// anything. Crags 6 to 20 hold more, and were deliberately never asked.
	if out.Count < MaxClimbs {
		t.Errorf("Count = %d, want at least %d", out.Count, MaxClimbs)
	}
}

// Stopping early must not turn a working search into an outage, nor hide one.
func TestFindClimbsFanOutFailures(t *testing.T) {
	t.Run("a partial failure still answers", func(t *testing.T) {
		gql, _ := routingStub(t, nearBody("a", "b", "c"), func(uuid string) (string, bool) {
			if uuid == "b" {
				return "", true
			}
			return areaWithClimbs(uuid, "US", climbJSON("Route "+uuid, ydsGrade("5.9"), 40, "trad")), false
		})

		_, out, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish"})
		if err != nil {
			t.Fatalf("one failed crag should not fail the call: %v", err)
		}
		if len(out.Climbs) != 2 {
			t.Errorf("got %v, want the two surviving crags' routes", names(out.Climbs))
		}
	})

	t.Run("every crag failing is an outage", func(t *testing.T) {
		gql, _ := routingStub(t, nearBody("a", "b"), func(string) (string, bool) { return "", true })

		_, _, err := HandleFindClimbs(gql, geo.NewGazetteer())(context.Background(), nil,
			FindClimbsArgs{Place: "Squamish"})
		if err == nil {
			t.Fatal("expected an error when every detail call fails")
		}
	})
}
