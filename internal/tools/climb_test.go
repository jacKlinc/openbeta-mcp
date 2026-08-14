package tools

import (
	"slices"
	"testing"
)

func TestPreferredGrade(t *testing.T) {
	tests := []struct {
		name      string
		grades    Grades
		isBoulder bool
		want      string
	}{
		{"route prefers yds", Grades{Yds: "5.10a", French: "6a"}, false, "5.10a"},
		{"boulder prefers vscale", Grades{Vscale: "V4", Yds: "5.12"}, true, "V4"},
		{"boulder falls back to font", Grades{Font: "6C"}, true, "6C"},
		{"route falls back through systems", Grades{Ewbank: "18"}, false, "18"},
		{"ice grade", Grades{Wi: "WI4"}, false, "WI4"},
		{"ungraded", Grades{}, false, ""},
		// A boulder problem carrying only a route grade should still report it
		// rather than appearing ungraded.
		{"boulder with only route grade", Grades{Yds: "5.11a"}, true, "5.11a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PreferredGrade(tt.grades, tt.isBoulder); got != tt.want {
				t.Errorf("PreferredGrade() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDisciplines(t *testing.T) {
	got := Disciplines(ClimbType{Sport: true, Trad: true})
	if !slices.Equal(got, []string{"sport", "trad"}) {
		t.Errorf("Disciplines() = %v, want [sport trad]", got)
	}
	if n := len(Disciplines(ClimbType{})); n != 0 {
		t.Errorf("no disciplines set should yield empty, got %d", n)
	}
}
