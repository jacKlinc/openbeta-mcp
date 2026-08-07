package openbeta

// Wire types: these mirror the OpenBeta GraphQL schema and exist only to decode
// responses. They are deliberately kept separate from the summary types returned
// to MCP clients (NFR-10) so an upstream schema change cannot silently reshape
// tool output.

type areaMetadata struct {
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Leaf      bool    `json:"leaf"`
	IsBoulder bool    `json:"isBoulder"`
}

type areaContent struct {
	Description string `json:"description"`
}

type climbType struct {
	Sport         bool `json:"sport"`
	Trad          bool `json:"trad"`
	Bouldering    bool `json:"bouldering"`
	Alpine        bool `json:"alpine"`
	Aid           bool `json:"aid"`
	TopRope       bool `json:"tr"`
	Ice           bool `json:"ice"`
	Mixed         bool `json:"mixed"`
	Snow          bool `json:"snow"`
	DeepWaterSolo bool `json:"deepwatersolo"`
}

// disciplines returns the enabled discipline names, in a stable order.
func (t climbType) disciplines() []string {
	all := []struct {
		on   bool
		name string
	}{
		{t.Sport, "sport"},
		{t.Trad, "trad"},
		{t.Bouldering, "bouldering"},
		{t.Alpine, "alpine"},
		{t.Aid, "aid"},
		{t.TopRope, "toprope"},
		{t.Ice, "ice"},
		{t.Mixed, "mixed"},
		{t.Snow, "snow"},
		{t.DeepWaterSolo, "deepwatersolo"},
	}
	var out []string
	for _, d := range all {
		if d.on {
			out = append(out, d.name)
		}
	}
	return out
}

type gradeType struct {
	YDS    string `json:"yds"`
	VScale string `json:"vscale"`
	Font   string `json:"font"`
	French string `json:"french"`
	UIAA   string `json:"uiaa"`
	Ewbank string `json:"ewbank"`
	WI     string `json:"wi"`
}

// preferred picks the grade most likely to be meaningful for a climb, given the
// grade systems OpenBeta populates per discipline. Empty when the climb is
// ungraded.
func (g gradeType) preferred(isBoulder bool) string {
	order := []string{g.YDS, g.French, g.Ewbank, g.UIAA, g.WI}
	if isBoulder {
		order = []string{g.VScale, g.Font}
	}
	for _, v := range order {
		if v != "" {
			return v
		}
	}
	// Fall back to any populated system rather than reporting a graded climb as
	// ungraded.
	for _, v := range []string{g.YDS, g.VScale, g.Font, g.French, g.UIAA, g.Ewbank, g.WI} {
		if v != "" {
			return v
		}
	}
	return ""
}

type climb struct {
	UUID   string    `json:"uuid"`
	Name   string    `json:"name"`
	FA     string    `json:"fa"`
	Length int       `json:"length"`
	Safety string    `json:"safety"`
	Type   climbType `json:"type"`
	Grades gradeType `json:"grades"`
}

type area struct {
	UUID         string       `json:"uuid"`
	AreaName     string       `json:"areaName"`
	TotalClimbs  int          `json:"totalClimbs"`
	GradeContext string       `json:"gradeContext"`
	PathTokens   []string     `json:"pathTokens"`
	Metadata     areaMetadata `json:"metadata"`
	Content      areaContent  `json:"content"`
	Children     []area       `json:"children"`
	Climbs       []climb      `json:"climbs"`
}

type cragsWithinData struct {
	CragsWithin []area `json:"cragsWithin"`
}

type areaData struct {
	Area *area `json:"area"`
}

// Output types: what MCP clients actually receive. Trimmed for token economy and
// signal-to-noise (NFR-11).

// CragSummary is one crag in a crags_within result.
type CragSummary struct {
	UUID       string   `json:"uuid"`
	Name       string   `json:"name"`
	Lat        float64  `json:"lat"`
	Lng        float64  `json:"lng"`
	ClimbCount int      `json:"climbCount"`
	IsBoulder  bool     `json:"isBoulder,omitempty"`
	Path       []string `json:"path,omitempty"`
}

// AreaDetail is the get_area_details result.
type AreaDetail struct {
	UUID        string         `json:"uuid"`
	Name        string         `json:"name"`
	Path        []string       `json:"path,omitempty"`
	Lat         float64        `json:"lat"`
	Lng         float64        `json:"lng"`
	Description string         `json:"description,omitempty"`
	GradeSystem string         `json:"gradeSystem,omitempty"`
	IsBoulder   bool           `json:"isBoulder,omitempty"`
	Climbs      []ClimbSummary `json:"climbs,omitempty"`
	Children    []ChildArea    `json:"children,omitempty"`
}

// ClimbSummary is one route within an area.
type ClimbSummary struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Grade       string   `json:"grade,omitempty"`
	Disciplines []string `json:"disciplines,omitempty"`
	Length      int      `json:"length,omitempty"`
	FA          string   `json:"fa,omitempty"`
	Safety      string   `json:"safety,omitempty"`
}

// ChildArea is a sub-area of an area that holds no climbs of its own.
type ChildArea struct {
	UUID string  `json:"uuid"`
	Name string  `json:"name"`
	Lat  float64 `json:"lat,omitempty"`
	Lng  float64 `json:"lng,omitempty"`
}
