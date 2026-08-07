package openbeta

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The central finding behind climbCount: Area.totalClimbs reads 0 on most leaf
// crags that have climbs. Filtering on it discards real crags — 143 of 176 in the
// measured Squamish bbox. These cases are taken from live responses.
func TestClimbCountIgnoresUnreliableTotalClimbs(t *testing.T) {
	tests := []struct {
		name string
		area area
		want int
	}{
		{
			// Tantalus Wall, a real multi-pitch wall.
			name: "leaf crag with climbs but totalClimbs 0",
			area: area{TotalClimbs: 0, Climbs: make([]climb, 8)},
			want: 8,
		},
		{
			// Stawamus Chief: climbs live on its 32 children.
			name: "parent area with totalClimbs and no climbs of its own",
			area: area{TotalClimbs: 369, Climbs: nil},
			want: 369,
		},
		{
			name: "genuinely empty area",
			area: area{TotalClimbs: 0, Climbs: nil},
			want: 0,
		},
		{
			// Apron Boulders, where the two agree.
			name: "both populated and in agreement",
			area: area{TotalClimbs: 55, Climbs: make([]climb, 55)},
			want: 55,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := climbCount(tt.area); got != tt.want {
				t.Errorf("climbCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCragsWithinDropsOnlyTrulyEmptyAreas(t *testing.T) {
	body := `{"data":{"cragsWithin":[
		{"uuid":"a","areaName":"Tantalus Wall","totalClimbs":0,"metadata":{"lat":49.68,"lng":-123.14},"climbs":[{"uuid":"c1"},{"uuid":"c2"}]},
		{"uuid":"b","areaName":"Western Dihedrals","totalClimbs":0,"metadata":{"lat":49.68,"lng":-123.14},"climbs":[]},
		{"uuid":"c","areaName":"The Apron","totalClimbs":51,"metadata":{"lat":49.67,"lng":-123.15},"climbs":[{"uuid":"c3"}]}
	]}}`
	c := newTestClient(t, 200, body)

	got, err := c.CragsWithin(context.Background(), BBox{-123.2, 49.6, -122.9, 49.8}, 13)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 crags (empty one dropped), got %d: %+v", len(got), got)
	}
	// Tantalus Wall must survive despite totalClimbs 0.
	var found bool
	for _, cr := range got {
		if cr.Name == "Tantalus Wall" {
			found = true
			if cr.ClimbCount != 2 {
				t.Errorf("Tantalus Wall ClimbCount = %d, want 2", cr.ClimbCount)
			}
		}
		if cr.Name == "Western Dihedrals" {
			t.Error("area with no climbs should have been dropped")
		}
	}
	if !found {
		t.Error("Tantalus Wall was dropped despite having climbs")
	}
}

func TestCragsWithinSortsByClimbCount(t *testing.T) {
	body := `{"data":{"cragsWithin":[
		{"uuid":"a","areaName":"small","totalClimbs":0,"metadata":{},"climbs":[{"uuid":"1"}]},
		{"uuid":"b","areaName":"big","totalClimbs":0,"metadata":{},"climbs":[{"uuid":"1"},{"uuid":"2"},{"uuid":"3"}]},
		{"uuid":"c","areaName":"medium","totalClimbs":0,"metadata":{},"climbs":[{"uuid":"1"},{"uuid":"2"}]}
	]}}`
	c := newTestClient(t, 200, body)

	got, err := c.CragsWithin(context.Background(), BBox{-123.2, 49.6, -122.9, 49.8}, 13)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"big", "medium", "small"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestBBoxIsSentInSchemaOrder(t *testing.T) {
	b := BBox{-123.2, 49.6, -122.9, 49.8}
	if b.MinLng() != -123.2 || b.MinLat() != 49.6 || b.MaxLng() != -122.9 || b.MaxLat() != 49.8 {
		t.Fatalf("bbox accessors disagree with [minLng, minLat, maxLng, maxLat]: %+v", b)
	}
}

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

// A parent area returns climbs: [] with children populated. The mapping must
// expose the children, or a caller sees a 369-route wall as empty.
func TestGetAreaExposesChildrenForParentArea(t *testing.T) {
	body := `{"data":{"area":{
		"uuid":"8f267065-fc1a-59ce-bcf1-6e9335548363",
		"areaName":"Stawamus Chief",
		"totalClimbs":369,
		"gradeContext":"US",
		"pathTokens":["Canada","British Columbia","Squamish","Stawamus Chief"],
		"metadata":{"lat":49.68,"lng":-123.14,"leaf":false},
		"content":{"description":"  The Chief.  "},
		"children":[{"uuid":"ch1","areaName":"The Apron","totalClimbs":51,"metadata":{"lat":49.67,"lng":-123.15}}],
		"climbs":[]
	}}}`
	c := newTestClient(t, 200, body)

	got, err := c.GetArea(context.Background(), "8f267065-fc1a-59ce-bcf1-6e9335548363")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Children) != 1 || got.Children[0].Name != "The Apron" {
		t.Errorf("children not exposed: %+v", got.Children)
	}
	if len(got.Climbs) != 0 {
		t.Errorf("expected no climbs on parent area, got %d", len(got.Climbs))
	}
	if got.Description != "The Chief." {
		t.Errorf("description not trimmed: %q", got.Description)
	}
	if got.GradeSystem != "US" {
		t.Errorf("GradeSystem = %q, want US", got.GradeSystem)
	}
}

func TestGetAreaMapsClimbs(t *testing.T) {
	body := `{"data":{"area":{
		"uuid":"leaf","areaName":"The Apron","totalClimbs":0,
		"metadata":{"lat":49.67,"lng":-123.15,"leaf":true},
		"content":{},
		"children":[],
		"climbs":[{"uuid":"c1","name":"Diedre","fa":"J. Baldwin","length":150,"safety":"UNSPECIFIED",
			"type":{"trad":true},"grades":{"yds":"5.8"}}]
	}}}`
	c := newTestClient(t, 200, body)

	got, err := c.GetArea(context.Background(), "8f267065-fc1a-59ce-bcf1-6e9335548363")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.Climbs) != 1 {
		t.Fatalf("expected 1 climb, got %d", len(got.Climbs))
	}
	cl := got.Climbs[0]
	if cl.Name != "Diedre" || cl.Grade != "5.8" || cl.Length != 150 || cl.FA != "J. Baldwin" {
		t.Errorf("climb mapped incorrectly: %+v", cl)
	}
	if len(cl.Disciplines) != 1 || cl.Disciplines[0] != "trad" {
		t.Errorf("disciplines = %v, want [trad]", cl.Disciplines)
	}
	// UNSPECIFIED is a placeholder, not a rating.
	if cl.Safety != "" {
		t.Errorf("Safety = %q, want empty for UNSPECIFIED", cl.Safety)
	}
}

// The API encodes missing values in-band. Passing them through would have an LLM
// report a first ascent as "unknown" and a length as -1 metres.
func TestPlaceholderValuesAreDropped(t *testing.T) {
	body := `{"data":{"area":{
		"uuid":"leaf","areaName":"Apron Boulders","totalClimbs":55,
		"metadata":{"lat":49.68,"lng":-123.14,"leaf":true,"isBoulder":true},
		"content":{},"children":[],
		"climbs":[{"uuid":"c1","name":"Cobra","fa":"unknown","length":-1,"safety":"UNSPECIFIED",
			"type":{"bouldering":true},"grades":{"vscale":"v1"}}]
	}}}`
	c := newTestClient(t, 200, body)

	got, err := c.GetArea(context.Background(), "8f267065-fc1a-59ce-bcf1-6e9335548363")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cl := got.Climbs[0]
	if cl.FA != "" {
		t.Errorf("FA = %q, want empty for the \"unknown\" placeholder", cl.FA)
	}
	if cl.Length != 0 {
		t.Errorf("Length = %d, want 0 for the -1 sentinel", cl.Length)
	}
	if cl.Safety != "" {
		t.Errorf("Safety = %q, want empty", cl.Safety)
	}
	// The real values around them must survive.
	if cl.Name != "Cobra" || cl.Grade != "v1" {
		t.Errorf("real fields lost: %+v", cl)
	}

	// All three are omitempty, so they vanish from the payload entirely.
	b, _ := json.Marshal(cl)
	for _, key := range []string{"fa", "length", "safety"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("placeholder field %q present in output: %s", key, b)
		}
	}
}
