// Package grade parses and compares climbing grades.
//
// One parser per system, and no conversions between them. Cross-system
// conversion is contested and lossy — sources disagree on whether 6b is 5.10c or
// 5.10d — so a silent conversion would turn a grade filter into a guess. A span
// carries the system it was read in, and comparing across systems is an error
// rather than a quiet non-match.
//
// Bouldering scales (vscale, font) and water ice (wi) are not parsed: the tools
// return roped rock only, and a range spanning WI4 and 5.10 has no meaning.
package grade

import (
	"fmt"
	"strings"
)

// System is a grading scale. The values match the field names OpenBeta uses on
// GradeType, so the mapping to a recorded grade string stays obvious.
type System string

const (
	YDS    System = "yds"
	French System = "french"
	UIAA   System = "uiaa"
)

// contexts maps an area's gradeContext to the system its grades are recorded in.
//
// Observed live: US, FR, UK, AU, UIAA. UK is absent deliberately — OpenBeta's
// GradeType has no British field at all, so a UK area cannot carry E-grades and
// there is nothing here to parse them into.
var contexts = map[string]System{
	"US":   YDS,
	"FR":   French,
	"UIAA": UIAA,
}

// SystemFor returns the system an area's grades are recorded in.
func SystemFor(gradeContext string) (System, bool) {
	s, ok := contexts[gradeContext]
	return s, ok
}

// Span is the range a recorded grade covers, in one system.
//
// Many grades in the data are not a single point: "5.10" names the whole letter
// range, "5.11a/b" straddles two, and only something like "5.10b" is exact.
// Keeping the imprecision explicit is what lets range matching stay honest —
// a route recorded as "5.10" genuinely might be 5.10d.
//
// Lo and Hi are ordinals on that system's own scale and are meaningless across
// systems, which is why System travels with them.
type Span struct {
	Lo     int
	Hi     int
	System System
}

// Overlaps reports whether two spans share any ground. Range filtering uses it
// so a route is included when any part of its grade could fall inside the
// requested range, rather than resolving ambiguity silently in one direction.
//
// Different systems are an error, not a non-match: 6a and 5.10a are not routes
// that fail to overlap, they are routes that cannot be compared. Returning false
// would exclude them and look like a filter working correctly.
func (s Span) Overlaps(other Span) (bool, error) {
	if s.System != other.System {
		return false, fmt.Errorf("cannot compare %s grade with %s grade", s.System, other.System)
	}
	return s.Lo <= other.Hi && other.Lo <= s.Hi, nil
}

// Parse reads a grade string in the given system.
func Parse(system System, s string) (Span, error) {
	switch system {
	case YDS:
		return ParseYDS(s)
	case French:
		return ParseFrench(s)
	case UIAA:
		return ParseUIAA(s)
	default:
		return Span{}, fmt.Errorf("no parser for %q", system)
	}
}

// Recorded picks the grade string a climb carries in the given system, from the
// several GradeType may populate.
func Recorded(system System, yds, french, uiaa string) string {
	switch system {
	case YDS:
		return yds
	case French:
		return french
	case UIAA:
		return uiaa
	default:
		return ""
	}
}

// systems lists every system a grade can be read in, each with an example of
// its notation, in the order error text should read them out.
var systems = []struct {
	System  System
	Example string
}{
	{YDS, "5.10b"},
	{French, "6a+"},
	{UIAA, "7-"},
}

// Systems returns the systems Parse understands.
func Systems() []System {
	out := make([]System, 0, len(systems))
	for _, s := range systems {
		out = append(out, s.System)
	}
	return out
}

// Examples renders the systems as "5.10b (yds), 6a+ (french), ...", for the
// error a caller reads when a bound is not a grade in any of them.
func Examples() string {
	parts := make([]string, 0, len(systems))
	for _, s := range systems {
		parts = append(parts, fmt.Sprintf("%s (%s)", s.Example, s.System))
	}
	return strings.Join(parts, ", ")
}

// ParsesIn returns every system in which s is a valid grade.
//
// A grade string does not reliably name its own system: "7" is a UIAA grade, an imprecise French one at once.
// So this reports candidates rather than choosing between them — an area's gradeContext does the choosing.
// This exists only to tell a typo apart from a grade written in a system the
// search happens not to reach.
func ParsesIn(s string) []System {
	var out []System
	for _, sys := range Systems() {
		if _, err := Parse(sys, s); err == nil {
			out = append(out, sys)
		}
	}
	return out
}
