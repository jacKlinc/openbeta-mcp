// Package grade parses and compares climbing grades.
//
// Only the Yosemite Decimal System for now, which is what OpenBeta populates for
// roped climbs in North America. Other systems in GradeType (font, french, uiaa,
// ewbank, wi) are untouched.
package grade

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Span is the range a recorded grade covers.
//
// Many grades in the data are not a single point: "5.10" names the whole letter
// range, "5.11a/b" straddles two, and only something like "5.10b" is exact.
// Keeping the imprecision explicit is what lets range matching stay honest —
// a route recorded as "5.10" genuinely might be 5.10d.
type Span struct {
	Lo int
	Hi int
}

// Overlaps reports whether two spans share any ground. Range filtering uses it
// so a route is included when any part of its grade could fall inside the
// requested range, rather than resolving ambiguity silently in one direction.
func (s Span) Overlaps(other Span) bool {
	return s.Lo <= other.Hi && other.Lo <= s.Hi
}

// ordinal packs a grade into a sortable integer: number*4 + letter index.
//
// The scale has no letter subdivisions below 5.10 — 5.9 is a single grade, not
// four — so numbers below 10 occupy one slot and letters only apply above.
func ordinal(number, letter int) int { return number*4 + letter }

// letterCount is the width of a bare grade at 5.10 and above: a, b, c, d.
const letterCount = 4

// ydsPattern matches the forms the API actually returns. Verified against live
// data: 5.9, 5.10b, 5.11a/b, 5.9+, 5.10-, 5.12.
//
// The modifier is captured but does not move the grade: +/- orders routes
// within a number, it does not promote one into the next.
var ydsPattern = regexp.MustCompile(`^5\.(\d{1,2})([a-d])?(?:/([a-d]))?([+-])?$`)

// ParseYDS turns a recorded grade string into the span it covers.
func ParseYDS(s string) (Span, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	m := ydsPattern.FindStringSubmatch(t)
	if m == nil {
		return Span{}, fmt.Errorf("not a YDS grade: %q", s)
	}

	number, err := strconv.Atoi(m[1])
	if err != nil {
		return Span{}, fmt.Errorf("not a YDS grade: %q", s)
	}

	// Below 5.10 there are no letters, so the grade is exact whatever else was
	// written. "5.9+" is still 5.9.
	if number < 10 {
		n := ordinal(number, 0)
		return Span{Lo: n, Hi: n}, nil
	}

	first, second := m[2], m[3]
	switch {
	case first == "":
		// A bare "5.10" could be anything from a to d, and often is — the
		// letter was simply never recorded.
		return Span{Lo: ordinal(number, 0), Hi: ordinal(number, letterCount-1)}, nil
	case second == "":
		n := ordinal(number, letterIndex(first))
		return Span{Lo: n, Hi: n}, nil
	default:
		lo, hi := letterIndex(first), letterIndex(second)
		if lo > hi {
			lo, hi = hi, lo
		}
		return Span{Lo: ordinal(number, lo), Hi: ordinal(number, hi)}, nil
	}
}

func letterIndex(s string) int {
	if s == "" {
		return 0
	}
	return int(s[0] - 'a')
}
