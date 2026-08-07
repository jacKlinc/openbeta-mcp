package openbeta

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// uuidPattern matches the canonical 8-4-4-4-12 hex form used for OpenBeta area
// and climb ids.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ErrAreaNotFound is returned when the API resolves the query but has no area
// with that UUID. Distinct from an upstream failure: the answer is "no such
// area", not "the lookup broke".
type ErrAreaNotFound struct {
	UUID string
}

func (e *ErrAreaNotFound) Error() string {
	return fmt.Sprintf("no area found with uuid %q", e.UUID)
}

// ValidateAreaID rejects malformed UUIDs before any upstream call (FR-18).
func ValidateAreaID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("areaId is required")
	}
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("areaId %q is not a valid UUID (expected 8-4-4-4-12 hex, e.g. 8f267065-fc1a-59ce-bcf1-6e9335548363)", id)
	}
	return nil
}

// GetArea returns detail for a single area.
//
// An area holds either climbs or child areas, never both: OpenBeta stores climbs
// only on leaf areas. Stawamus Chief reports totalClimbs 369 with an empty climbs
// array and 32 children, so a caller that reads only Climbs would conclude a
// 369-route wall is empty. Both fields are populated here, and Children is how a
// caller descends.
func (c *Client) GetArea(ctx context.Context, uuid string) (*AreaDetail, error) {
	if err := ValidateAreaID(uuid); err != nil {
		return nil, err
	}

	var data areaData
	if err := c.execute(ctx, queryArea, map[string]any{"uuid": uuid}, &data); err != nil {
		return nil, err
	}
	if data.Area == nil {
		return nil, &ErrAreaNotFound{UUID: uuid}
	}
	a := data.Area

	detail := &AreaDetail{
		UUID:        a.UUID,
		Name:        a.AreaName,
		Path:        a.PathTokens,
		Lat:         a.Metadata.Lat,
		Lng:         a.Metadata.Lng,
		Description: strings.TrimSpace(a.Content.Description),
		GradeSystem: a.GradeContext,
		IsBoulder:   a.Metadata.IsBoulder,
	}

	for _, cl := range a.Climbs {
		detail.Climbs = append(detail.Climbs, ClimbSummary{
			UUID:        cl.UUID,
			Name:        cl.Name,
			Grade:       cl.Grades.preferred(cl.Type.Bouldering || a.Metadata.IsBoulder),
			Disciplines: cl.Type.disciplines(),
			Length:      normalizeLength(cl.Length),
			FA:          normalizeFA(cl.FA),
			Safety:      normalizeSafety(cl.Safety),
		})
	}

	for _, ch := range a.Children {
		detail.Children = append(detail.Children, ChildArea{
			UUID: ch.UUID,
			Name: ch.AreaName,
			Lat:  ch.Metadata.Lat,
			Lng:  ch.Metadata.Lng,
		})
	}

	return detail, nil
}

// The API encodes "not recorded" with in-band placeholder values rather than
// nulls. Passing these through would have an LLM report a climb's first ascent
// as literally "unknown" and its length as -1 metres, so they are mapped to zero
// values and omitted from the JSON.

// normalizeSafety drops the placeholder so an unrated climb reports no safety
// rating rather than the literal string "UNSPECIFIED".
func normalizeSafety(s string) string {
	if s == "UNSPECIFIED" {
		return ""
	}
	return s
}

// normalizeFA drops the "unknown" placeholder used when no first ascent is
// recorded.
func normalizeFA(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "unknown") {
		return ""
	}
	return s
}

// normalizeLength drops the -1 sentinel used when a climb's length is unrecorded.
func normalizeLength(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
