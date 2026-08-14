package tools

import (
	"strings"
	"testing"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

func TestNewBBoxRejectsWrongLength(t *testing.T) {
	for _, in := range [][]float64{{}, {1}, {1, 2, 3}, {1, 2, 3, 4, 5}} {
		if _, err := NewBBox(in); err == nil {
			t.Errorf("NewBBox(%v) = nil error, want error", in)
		}
	}
}

func TestBBoxValidate(t *testing.T) {
	tests := []struct {
		name    string
		bbox    BBox
		wantErr string
	}{
		{"squamish", BBox{-123.2, 49.6, -122.9, 49.8}, ""},
		{"antimeridian west edge", BBox{-180, -90, 180, 90}, ""},
		{"lng swapped", BBox{-122.9, 49.6, -123.2, 49.8}, "minLng"},
		{"lat swapped", BBox{-123.2, 49.8, -122.9, 49.6}, "minLat"},
		{"lng out of range", BBox{-181, 49.6, -122.9, 49.8}, "longitude out of range"},
		{"lat out of range", BBox{-123.2, 49.6, -122.9, 91}, "latitude out of range"},
		// A lat/lng ordering mistake is the most likely caller error, and it
		// happens to be caught by the range check.
		{"lat/lng transposed", BBox{49.6, -123.2, 49.8, -122.9}, "latitude out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.bbox.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

// The central finding behind climbCount: Area.totalClimbs reads 0 on most leaf
// crags that have climbs. Filtering on it discards real crags — 143 of 176 in the
// measured Squamish bbox. These cases are taken from live responses.
func TestClimbCountIgnoresUnreliableTotalClimbs(t *testing.T) {
	tests := []struct {
		name string
		area generated.CragsWithinCragsWithinArea
		want int
	}{
		{
			// Tantalus Wall, a real multi-pitch wall.
			name: "leaf crag with climbs but totalClimbs 0",
			area: generated.CragsWithinCragsWithinArea{TotalClimbs: 0, Climbs: make([]generated.CragsWithinCragsWithinAreaClimbsClimb, 8)},
			want: 8,
		},
		{
			// Stawamus Chief: climbs live on its 32 children.
			name: "parent area with totalClimbs and no climbs of its own",
			area: generated.CragsWithinCragsWithinArea{TotalClimbs: 369, Climbs: nil},
			want: 369,
		},
		{
			name: "genuinely empty area",
			area: generated.CragsWithinCragsWithinArea{TotalClimbs: 0, Climbs: nil},
			want: 0,
		},
		{
			// Apron Boulders, where the two agree.
			name: "both populated and in agreement",
			area: generated.CragsWithinCragsWithinArea{TotalClimbs: 55, Climbs: make([]generated.CragsWithinCragsWithinAreaClimbsClimb, 55)},
			want: 55,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClimbCount(tt.area); got != tt.want {
				t.Errorf("climbCount() = %d, want %d", got, tt.want)
			}
		})
	}
}
