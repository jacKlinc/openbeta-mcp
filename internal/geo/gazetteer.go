package geo

import (
	"context"
	"strings"
)

// Gazetteer resolves well-known climbing destinations from a table compiled into
// the binary.
//
// Static data rather than a lookup service: these places do not move, it covers
// the large majority of real queries, and it keeps the single-binary,
// no-API-key property that makes `go install` and run work. A geocoder for
// everything else goes behind Resolver later.
type Gazetteer struct {
	places map[string]Point
}

// NewGazetteer returns a Gazetteer over the built-in table.
func NewGazetteer() *Gazetteer {
	g := &Gazetteer{places: make(map[string]Point, len(destinations))}
	for name, p := range destinations {
		g.places[normalize(name)] = p
	}
	return g
}

// Resolve implements Resolver. Unknown names return *ErrPlaceUnknown.
func (g *Gazetteer) Resolve(_ context.Context, place string) (Point, error) {
	if p, ok := g.places[normalize(place)]; ok {
		return p, nil
	}
	return Point{}, &ErrPlaceUnknown{Place: place}
}

// accents covers the diacritics that appear in climbing place names. A full
// Unicode fold would mean depending on golang.org/x/text for a handful of
// characters, which is not worth a module dependency in a single-binary tool.
var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ß", "ss",
)

// normalize folds the variations a model or a person will type: case, padding
// and accents. "Céüse", "ceuse" and " CEUSE " are one key.
func normalize(s string) string {
	// Lowercase first so the replacer only needs the lowercase forms.
	return strings.Join(strings.Fields(accents.Replace(strings.ToLower(s))), " ")
}

// destinations are climbing towns and areas, not precise crag coordinates — the
// point is a search origin, and cragsNear takes a radius from it.
//
// Keys are written naturally; normalize handles the variants.
var destinations = map[string]Point{
	// North America
	"Squamish":        {49.7016, -123.1558},
	"Canmore":         {51.0890, -115.3594},
	"Banff":           {51.1784, -115.5708},
	"Bishop":          {37.3614, -118.3951},
	"Yosemite":        {37.7345, -119.6027},
	"Joshua Tree":     {33.8734, -115.9010},
	"Red Rocks":       {36.1350, -115.4270},
	"Indian Creek":    {38.0264, -109.5420},
	"Moab":            {38.5733, -109.5498},
	"Index":           {47.8207, -121.5551},
	"Leavenworth":     {47.5962, -120.6615},
	"Smith Rock":      {44.3684, -121.1406},
	"Red River Gorge": {37.7834, -83.6824},
	"New River Gorge": {38.0699, -81.0818},
	"The Gunks":       {41.7370, -74.1929},
	"Rumney":          {43.8073, -71.8342},
	"Boulder":         {40.0150, -105.2705},
	"Estes Park":      {40.3772, -105.5217},
	"Ten Sleep":       {44.0322, -107.4479},
	"Lander":          {42.8330, -108.7307},
	"Hueco Tanks":     {31.9226, -106.0442},
	"Potrero Chico":   {25.9333, -100.4833},
	"Skaha":           {49.4667, -119.5833},

	// Europe
	"Chamonix":      {45.9237, 6.8694},
	"Fontainebleau": {48.4047, 2.7016},
	"Ceuse":         {44.5167, 5.9333},
	"Verdon":        {43.7500, 6.3333},
	"Siurana":       {41.2606, 0.9331},
	"Margalef":      {41.2925, 0.7658},
	"El Chorro":     {36.9053, -4.7594},
	"Rodellar":      {42.3011, -0.1428},
	"Arco":          {45.9186, 10.8853},
	"Finale Ligure": {44.1697, 8.3439},
	"Kalymnos":      {36.9500, 26.9833},
	"Leonidio":      {37.1636, 22.8564},
	"Frankenjura":   {49.7500, 11.3333},
	"Magic Wood":    {46.4167, 9.4333},
	"Interlaken":    {46.6863, 7.8632},
	"Peak District": {53.3400, -1.7800},
	"Portland":      {50.5400, -2.4400},
	"Fair Head":     {55.2231, -6.1503},

	// Rest of world
	"Railay":         {8.0056, 98.8378},
	"Yangshuo":       {24.7784, 110.4972},
	"Blue Mountains": {-33.7000, 150.3000},
	"Grampians":      {-37.1500, 142.4500},
	"Rocklands":      {-32.1667, 19.1667},
	"Cochamo":        {-41.4833, -72.3000},
	"Frey":           {-41.1833, -71.4667},
}
