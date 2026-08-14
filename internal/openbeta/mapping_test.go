package openbeta

import (
	"testing"
)

func TestPreferredGrade(t *testing.T) {
	tests := []struct {
		name      string
		grades    gradeType
		isBoulder bool
		want      string
	}{
		{"route prefers yds", gradeType{YDS: "5.10a", French: "6a"}, false, "5.10a"},
		{"boulder prefers vscale", gradeType{VScale: "V4", YDS: "5.12"}, true, "V4"},
		{"boulder falls back to font", gradeType{Font: "6C"}, true, "6C"},
		{"route falls back through systems", gradeType{Ewbank: "18"}, false, "18"},
		{"ice grade", gradeType{WI: "WI4"}, false, "WI4"},
		{"ungraded", gradeType{}, false, ""},
		// A boulder problem carrying only a route grade should still report it
		// rather than appearing ungraded.
		{"boulder with only route grade", gradeType{YDS: "5.11a"}, true, "5.11a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grades.preferred(tt.isBoulder); got != tt.want {
				t.Errorf("preferred() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDisciplines(t *testing.T) {
	got := climbType{Sport: true, Trad: true}.disciplines()
	if len(got) != 2 || got[0] != "sport" || got[1] != "trad" {
		t.Errorf("disciplines() = %v, want [sport trad]", got)
	}
	if n := len(climbType{}.disciplines()); n != 0 {
		t.Errorf("no disciplines set should yield empty, got %d", n)
	}
}
