package tools

import "github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"

// Grade and discipline logic for climbs returned by get_area_details.
//
// Both live here rather than on the generated types, which are regenerated from
// the schema and cannot carry methods.

// Grades is the set of grade systems a climb may carry. The upstream schema
// returns all seven on every climb with at most a couple populated, so choosing
// among them is the caller's problem — see PreferredGrade.
type Grades = generated.GetAreaDetailsAreaClimbsClimbGradesGradeType

// ClimbType is the set of discipline flags a climb may carry.
type ClimbType = generated.GetAreaDetailsAreaClimbsClimbType

// PreferredGrade picks the grade most likely to be meaningful for a climb, given
// the systems OpenBeta populates per discipline. Empty when the climb is
// ungraded.
//
// isBoulder comes from the area's metadata rather than the climb, since that is
// what upstream keys the grade system off.
func PreferredGrade(g Grades, isBoulder bool) string {
	order := []string{g.Yds, g.French, g.Ewbank, g.Uiaa, g.Wi}
	if isBoulder {
		order = []string{g.Vscale, g.Font}
	}
	for _, v := range order {
		if v != "" {
			return v
		}
	}
	// Fall back to any populated system rather than reporting a graded climb as
	// ungraded.
	for _, v := range []string{g.Yds, g.Vscale, g.Font, g.French, g.Uiaa, g.Ewbank, g.Wi} {
		if v != "" {
			return v
		}
	}
	return ""
}

// Disciplines returns the enabled discipline names, in a stable order.
func Disciplines(t ClimbType) []string {
	all := []struct {
		on   bool
		name string
	}{
		{t.Sport, "sport"},
		{t.Trad, "trad"},
		{t.Bouldering, "bouldering"},
		{t.Alpine, "alpine"},
		{t.Aid, "aid"},
		{t.Tr, "toprope"},
		{t.Ice, "ice"},
		{t.Mixed, "mixed"},
		{t.Snow, "snow"},
		{t.Deepwatersolo, "deepwatersolo"},
	}
	var out []string
	for _, d := range all {
		if d.on {
			out = append(out, d.name)
		}
	}
	return out
}
