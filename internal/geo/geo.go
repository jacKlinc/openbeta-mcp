// Package geo resolves place names to coordinates and measures distance between
// them.
//
// It exists because cragsNear's placeId argument is cosmetic — the resolver
// echoes it back untouched — so lnglat is the only input that selects anything,
// and every natural-language query has to become a point first.
package geo

import (
	"context"
	"fmt"
	"math"
)

// Point is a coordinate pair. Field order is lat, lng; the OpenBeta Point input
// type uses the same names, so the two map directly.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Resolver turns a place name into coordinates.
//
// One implementation today, Gazetteer. A geocoder for the long tail goes behind
// this interface later — see docs/gazzetteer/README.md — so call sites do not
// change when it lands.
type Resolver interface {
	Resolve(ctx context.Context, place string) (Point, error)
}

// ErrPlaceUnknown reports a name the resolver does not hold. Callers surface it
// as a request to supply coordinates directly rather than as a failure.
type ErrPlaceUnknown struct {
	Place string
}

func (e *ErrPlaceUnknown) Error() string {
	return fmt.Sprintf("no coordinates known for %q; pass lnglat instead", e.Place)
}

// earthRadiusKm is the mean radius. Good to ~0.5% for the distances involved
// here, which is far finer than crag coordinates are recorded to.
const earthRadiusKm = 6371.0

// Haversine returns the great-circle distance between two points, in kilometres.
func Haversine(a, b Point) float64 {
	lat1, lat2 := radians(a.Lat), radians(b.Lat)
	dLat := radians(b.Lat - a.Lat)
	dLng := radians(b.Lng - a.Lng)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLng/2)*math.Sin(dLng/2)

	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

func radians(deg float64) float64 { return deg * math.Pi / 180 }
