// Package grade parses and compares climbing grades, one system at a time.
//
// No cross-system conversion: sources disagree on whether 6b is 5.10c or 5.10d,
// so converting would turn a filter into a guess.
package grade

import (
	"fmt"
	"strings"
)

// Values match OpenBeta's GradeType field names.
type System string

const (
	YDS    System = "yds"
	French System = "french"
	UIAA   System = "uiaa"
)

// UK is absent deliberately: GradeType has no British field to parse.
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

// Span is the range a grade covers; Lo and Hi are ordinals on System's scale.
type Span struct {
	Lo     int
	Hi     int
	System System
}

// Different systems error rather than return false, which would look like a match test.
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

// Picks the grade string for one system, of the several GradeType may populate.
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

// Ordered as error text should read them out.
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

// Renders "5.10b (yds), 6a+ (french), ..." for the not-a-grade error.
func Examples() string {
	parts := make([]string, 0, len(systems))
	for _, s := range systems {
		parts = append(parts, fmt.Sprintf("%s (%s)", s.Example, s.System))
	}
	return strings.Join(parts, ", ")
}

// Candidates, not a choice: "7" is both UIAA and French.
func ParsesIn(s string) []System {
	var out []System
	for _, sys := range Systems() {
		if _, err := Parse(sys, s); err == nil {
			out = append(out, sys)
		}
	}
	return out
}
