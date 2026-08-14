package geo

import (
	"context"
	"errors"
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
