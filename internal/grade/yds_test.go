package grade

import "testing"

// Every form in this table was observed in live OpenBeta data while designing
// the filter, so none of them is hypothetical.
func TestParseYDS(t *testing.T) {
	tests := []struct {
		in      string
		wantLo  string // the equivalent exact grade at each end, for readability
		wantHi  string
		wantErr bool
	}{
		{in: "5.9", wantLo: "5.9", wantHi: "5.9"},
		{in: "5.6", wantLo: "5.6", wantHi: "5.6"},
		{in: "5.10b", wantLo: "5.10b", wantHi: "5.10b"},
		{in: "5.13d", wantLo: "5.13d", wantHi: "5.13d"},
		// Bare numbers at 5.10 and above span the letters, because the letter
		// was never recorded rather than because the route lacks one.
		{in: "5.10", wantLo: "5.10a", wantHi: "5.10d"},
		{in: "5.12", wantLo: "5.12a", wantHi: "5.12d"},
		{in: "5.11a/b", wantLo: "5.11a", wantHi: "5.11b"},
		// +/- order routes within a grade; they do not reach the next one.
		{in: "5.9+", wantLo: "5.9", wantHi: "5.9"},
		{in: "5.8-", wantLo: "5.8", wantHi: "5.8"},
		{in: "5.10-", wantLo: "5.10a", wantHi: "5.10d"},
		{in: " 5.10B ", wantLo: "5.10b", wantHi: "5.10b"},
		{in: "", wantErr: true},
		{in: "V4", wantErr: true},
		{in: "6a", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseYDS(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseYDS(%q) = %+v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseYDS(%q): %v", tt.in, err)
			}
			lo := mustParse(t, tt.wantLo)
			hi := mustParse(t, tt.wantHi)
			if got.Lo != lo.Lo || got.Hi != hi.Hi {
				t.Errorf("ParseYDS(%q) = %+v, want Lo of %s and Hi of %s (%d..%d)",
					tt.in, got, tt.wantLo, tt.wantHi, lo.Lo, hi.Hi)
			}
		})
	}
}

// The inclusive edge rule: a grade is in range when any part of it could be.
func TestSpanOverlaps(t *testing.T) {
	rangeLo := mustParse(t, "5.8")
	rangeHi := mustParse(t, "5.10b")
	want := Span{Lo: rangeLo.Lo, Hi: rangeHi.Hi, System: YDS}

	tests := []struct {
		grade string
		in    bool
	}{
		{"5.8", true},
		{"5.9", true},
		{"5.10a", true},
		{"5.10b", true},
		// Ambiguous grades count when they might fall inside, which is the whole
		// point of the rule.
		{"5.10", true},
		{"5.10a/b", true},
		{"5.10-", true},
		{"5.10c", false},
		{"5.7", false},
		{"5.11a", false},
	}
	for _, tt := range tests {
		t.Run(tt.grade, func(t *testing.T) {
			got := mustParse(t, tt.grade)
			in, err := got.Overlaps(want)
			if err != nil {
				t.Fatalf("%q: %v", tt.grade, err)
			}
			if in != tt.in {
				t.Errorf("%q (%+v) in 5.8..5.10b = %v, want %v", tt.grade, got, in, tt.in)
			}
		})
	}
}

func mustParse(t *testing.T, s string) Span {
	t.Helper()
	got, err := ParseYDS(s)
	if err != nil {
		t.Fatalf("ParseYDS(%q): %v", s, err)
	}
	return got
}
