package grade

import "testing"

func TestParseFrench(t *testing.T) {
	tests := []struct {
		in      string
		wantLo  string // the equivalent exact grade at each end, for readability
		wantHi  string
		wantErr bool
	}{
		{in: "6a", wantLo: "6a", wantHi: "6a"},
		{in: "6a+", wantLo: "6a+", wantHi: "6a+"},
		{in: "7c+", wantLo: "7c+", wantHi: "7c+"},
		// A bare number spans its letters. French does use bare numbers as
		// grades low down, which is exactly why a bare one cannot be read as
		// exact: "5" might be the grade 5, or a 5b whose letter went unrecorded.
		{in: "4", wantLo: "4a", wantHi: "4c+"},
		{in: "6", wantLo: "6a", wantHi: "6c+"},
		{in: "6b/6c", wantLo: "6b", wantHi: "6c"},
		{in: "6a/b", wantLo: "6a", wantHi: "6b"},
		{in: "6c/7a", wantLo: "6c", wantHi: "7a"},
		{in: " 6B ", wantLo: "6b", wantHi: "6b"},
		{in: "", wantErr: true},
		{in: "5.10a", wantErr: true},
		{in: "V4", wantErr: true},
		{in: "6d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseFrench(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseFrench(%q) = %+v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFrench(%q): %v", tt.in, err)
			}
			if got.System != French {
				t.Errorf("ParseFrench(%q).System = %q, want %q", tt.in, got.System, French)
			}
			lo := mustParseFrench(t, tt.wantLo)
			hi := mustParseFrench(t, tt.wantHi)
			if got.Lo != lo.Lo || got.Hi != hi.Hi {
				t.Errorf("ParseFrench(%q) = %+v, want Lo of %s and Hi of %s (%d..%d)",
					tt.in, got, tt.wantLo, tt.wantHi, lo.Lo, hi.Hi)
			}
		})
	}
}

// The + is a step of its own in French, unlike the YDS +/-. Ordering is what
// range filtering rests on, so it is pinned rather than assumed.
func TestFrenchPlusIsItsOwnStep(t *testing.T) {
	ascending := []string{"5c", "5c+", "6a", "6a+", "6b", "6b+", "6c", "6c+", "7a"}
	for i := 1; i < len(ascending); i++ {
		prev := mustParseFrench(t, ascending[i-1])
		next := mustParseFrench(t, ascending[i])
		if prev.Hi >= next.Lo {
			t.Errorf("%s (%+v) does not sort below %s (%+v)", ascending[i-1], prev, ascending[i], next)
		}
	}
}

func mustParseFrench(t *testing.T, s string) Span {
	t.Helper()
	got, err := ParseFrench(s)
	if err != nil {
		t.Fatalf("ParseFrench(%q): %v", s, err)
	}
	return got
}
