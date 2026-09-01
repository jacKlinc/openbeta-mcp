package grade

import "testing"

func TestParseUIAA(t *testing.T) {
	tests := []struct {
		in      string
		wantLo  string
		wantHi  string
		wantErr bool
	}{
		// A bare number is exact, unlike a bare YDS "5.10": 7 is a grade
		// sitting between 7- and 7+, not a letter nobody wrote down.
		{in: "7", wantLo: "7", wantHi: "7"},
		{in: "7-", wantLo: "7-", wantHi: "7-"},
		{in: "7+", wantLo: "7+", wantHi: "7+"},
		{in: "10", wantLo: "10", wantHi: "10"},
		{in: "6/6+", wantLo: "6", wantHi: "6+"},
		{in: "7-/7", wantLo: "7-", wantHi: "7"},
		{in: " 6+ ", wantLo: "6+", wantHi: "6+"},
		{in: "", wantErr: true},
		// Roman numerals are not supported upstream, so they are bad data here
		// rather than a form to accept.
		{in: "VII-", wantErr: true},
		{in: "5.10a", wantErr: true},
		{in: "6a", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseUIAA(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseUIAA(%q) = %+v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseUIAA(%q): %v", tt.in, err)
			}
			if got.System != UIAA {
				t.Errorf("ParseUIAA(%q).System = %q, want %q", tt.in, got.System, UIAA)
			}
			lo := mustParseUIAA(t, tt.wantLo)
			hi := mustParseUIAA(t, tt.wantHi)
			if got.Lo != lo.Lo || got.Hi != hi.Hi {
				t.Errorf("ParseUIAA(%q) = %+v, want Lo of %s and Hi of %s (%d..%d)",
					tt.in, got, tt.wantLo, tt.wantHi, lo.Lo, hi.Hi)
			}
		})
	}
}

// -, bare and + are three grades, so they must sort as three.
func TestUIAAModifiersAreSteps(t *testing.T) {
	ascending := []string{"5", "5+", "6-", "6", "6+", "7-", "7"}
	for i := 1; i < len(ascending); i++ {
		prev := mustParseUIAA(t, ascending[i-1])
		next := mustParseUIAA(t, ascending[i])
		if prev.Hi >= next.Lo {
			t.Errorf("%s (%+v) does not sort below %s (%+v)", ascending[i-1], prev, ascending[i], next)
		}
	}
}

func mustParseUIAA(t *testing.T, s string) Span {
	t.Helper()
	got, err := ParseUIAA(s)
	if err != nil {
		t.Fatalf("ParseUIAA(%q): %v", s, err)
	}
	return got
}
