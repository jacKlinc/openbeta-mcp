package openbeta

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
