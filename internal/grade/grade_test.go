package grade

import (
	"slices"
	"strings"
	"testing"
)

// The rule the whole package exists to enforce: a span from one system compared
// against another is an error, never a quiet false. Returning false would drop
// the route and look exactly like a filter working.
func TestOverlapsAcrossSystemsIsAnError(t *testing.T) {
	yds, err := ParseYDS("5.10a")
	if err != nil {
		t.Fatalf("ParseYDS: %v", err)
	}
	french, err := ParseFrench("6a")
	if err != nil {
		t.Fatalf("ParseFrench: %v", err)
	}

	got, err := yds.Overlaps(french)
	if err == nil {
		t.Fatalf("5.10a vs 6a = %v with no error, want an error", got)
	}
	if got {
		t.Error("a failed comparison must not also report an overlap")
	}
	// The message has to name both systems: it reaches a caller who wrote one
	// of them by mistake.
	for _, want := range []string{string(YDS), string(French)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	// The zero System is a system too, so an uninitialised Span cannot slip past
	// the check and compare against everything.
	if _, err := yds.Overlaps(Span{Lo: 0, Hi: 1 << 30}); err == nil {
		t.Error("comparing against a Span with no system must fail")
	}
}

// A grade string does not name its own system, so nothing may guess one. These
// are the collisions that make guessing wrong rather than merely unprincipled.
func TestGradeStringsAreAmbiguousAcrossSystems(t *testing.T) {
	tests := []struct {
		in   string
		want []System
	}{
		// "7" is a UIAA grade and an imprecise French one.
		{"7", []System{French, UIAA}},
		// "7a" narrows to French among the systems parsed here — Fontainebleau
		// writes it too, but font is a bouldering scale and is deliberately not
		// parsed, so it cannot collide.
		{"7a", []System{French}},
		{"5.10a", []System{YDS}},
		{"6a+", []System{French}},
		{"V4", nil},
		{"E4 6a", nil},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := ParsesIn(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ParsesIn(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// Context picks the parser, and the ambiguous strings above resolve differently
// depending on which area they are read in.
func TestSystemForResolvesAmbiguity(t *testing.T) {
	tests := []struct {
		gradeContext string
		want         System
		known        bool
	}{
		{"US", YDS, true},
		{"FR", French, true},
		{"UIAA", UIAA, true},
		// OpenBeta's GradeType has no British field, so a UK area cannot carry
		// an E-grade and there is nothing to parse one into. Observed live
		// alongside the four above.
		{"UK", "", false},
		{"", "", false},
		{"ZZ", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.gradeContext, func(t *testing.T) {
			got, ok := SystemFor(tt.gradeContext)
			if ok != tt.known || got != tt.want {
				t.Errorf("SystemFor(%q) = %q, %v; want %q, %v", tt.gradeContext, got, ok, tt.want, tt.known)
			}
		})
	}

	// The same string, read in two contexts, is two different grades — and in a
	// third it is not a grade at all. This is what area-driven selection buys.
	_, err := Parse(French, "7")
	if err != nil {
		t.Fatalf("Parse(french, \"7\"): %v", err)
	}
	if _, err := Parse(YDS, "7"); err == nil {
		t.Error("Parse(yds, \"7\") should fail rather than guessing a number")
	}
}

// Recorded picks the field for the system, so a French area is filtered on the
// French grade rather than on whatever yds happens to hold.
func TestRecorded(t *testing.T) {
	const (
		yds    = "5.10a"
		french = "6a+"
		uiaa   = "7-"
	)
	tests := []struct {
		system System
		want   string
	}{
		{YDS, yds},
		{French, french},
		{UIAA, uiaa},
		{System("font"), ""},
		{System(""), ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.system), func(t *testing.T) {
			if got := Recorded(tt.system, yds, french, uiaa); got != tt.want {
				t.Errorf("Recorded(%q) = %q, want %q", tt.system, got, tt.want)
			}
		})
	}
}

// Parse must refuse a system it has no parser for rather than falling through
// to a default, which would silently read every grade as that default.
func TestParseRejectsUnknownSystem(t *testing.T) {
	for _, system := range []System{"font", "vscale", "wi", ""} {
		if _, err := Parse(System(system), "5.10a"); err == nil {
			t.Errorf("Parse(%q, \"5.10a\") succeeded, want an error", system)
		}
	}
}

// Examples reaches a caller who wrote a bound wrong, so it has to show every
// system that would have worked.
func TestExamplesCoverEverySystem(t *testing.T) {
	got := Examples()
	for _, system := range Systems() {
		if !strings.Contains(got, string(system)) {
			t.Errorf("Examples() = %q, missing %q", got, system)
		}
	}
	for _, s := range systems {
		if parsed := ParsesIn(s.Example); !slices.Contains(parsed, s.System) {
			t.Errorf("example %q for %q does not parse as one", s.Example, s.System)
		}
	}
}
