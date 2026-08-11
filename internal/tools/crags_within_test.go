package tools

import (
	"strings"
	"testing"
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
