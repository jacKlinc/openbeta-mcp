package geo

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGazetteerResolve(t *testing.T) {
	g := NewGazetteer()

	tests := []struct {
		name  string
		place string
		want  Point
		found bool
	}{
		{"known place", "Squamish", Point{49.7016, -123.1558}, true},
		// Whatever a model types should hit the same entry.
		{"case and padding", "  SQUAMISH ", Point{49.7016, -123.1558}, true},
		{"accents folded", "Céüse", Point{44.5167, 5.9333}, true},
		{"unknown", "Nowhere At All", Point{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Resolve(context.Background(), tt.place)
			if tt.found {
				if err != nil {
					t.Fatalf("Resolve(%q): %v", tt.place, err)
				}
				if got != tt.want {
					t.Errorf("Resolve(%q) = %+v, want %+v", tt.place, got, tt.want)
				}
				return
			}
			// An unknown place is a typed error so the handler can tell the
			// model to pass coordinates instead of reporting a failure.
			var unknown *ErrPlaceUnknown
			if !errors.As(err, &unknown) {
				t.Fatalf("Resolve(%q) error = %v, want *ErrPlaceUnknown", tt.place, err)
			}
		})
	}
}

// TODO: test Haversine precision against more pairs if distances ever look off.
// Squamish to Vancouver is ~55km and is enough to catch a wrong radius constant
// or a lat/lng swap.
func TestHaversine(t *testing.T) {
	squamish := Point{49.7016, -123.1558}
	vancouver := Point{49.2827, -123.1207}

	if d := Haversine(squamish, vancouver); d < 45 || d > 55 {
		t.Errorf("Squamish to Vancouver = %.1f km, want roughly 47", d)
	}
	if d := Haversine(squamish, squamish); d != 0 {
		t.Errorf("distance to itself = %v, want 0", d)
	}
}

// The range check moved here from the tools package, so the rejection cases live
// with it. A transposed pair is the mistake this catches in practice: latitude
// stops at 90, so a longitude in that slot usually falls outside.
func TestNewPoint(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lng     float64
		wantErr string
	}{
		{name: "Squamish", lat: 49.7016, lng: -123.1558},
		{name: "null island", lat: 0, lng: 0},
		{name: "transposed", lat: -123.1558, lng: 49.7016, wantErr: "latitude out of range"},
		{name: "longitude too far east", lat: 0, lng: 181, wantErr: "longitude out of range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewPoint(tt.lat, tt.lng)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("NewPoint(%g, %g) = %+v, want an error", tt.lat, tt.lng, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q does not explain the problem (want %q)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewPoint(%g, %g): %v", tt.lat, tt.lng, err)
			}
			if got.Lat != tt.lat || got.Lng != tt.lng {
				t.Errorf("NewPoint(%g, %g) = %+v", tt.lat, tt.lng, got)
			}
		})
	}
}
