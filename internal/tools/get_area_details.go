package tools

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// GetAreaDetailsArgs is the input schema for get_area_details.
type GetAreaDetailsArgs struct {
	AreaID string `json:"areaId" jsonschema:"The area's UUID, in 8-4-4-4-12 hex form. Obtain one from a crags_within result or from an earlier get_area_details children list."`
}

func HandleGetAreaDetails(gqlClient *graphql.Client) mcp.ToolHandlerFor[GetAreaDetailsArgs, generated.GetAreaDetailsResponse] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args GetAreaDetailsArgs) (*mcp.CallToolResult, generated.GetAreaDetailsResponse, error) {
		// Validated here rather than upstream: the API answers a malformed UUID
		// with "area Invalid UUID.", which does not tell a model what to fix.
		if _, err := uuid.Parse(args.AreaID); err != nil {
			return nil, generated.GetAreaDetailsResponse{}, fmt.Errorf("areaId %q is not a valid UUID: expected 8-4-4-4-12 hex form, e.g. 8f267065-fc1a-59ce-bcf1-6e9335548363", args.AreaID)
		}

		area, err := generated.GetAreaDetails(ctx, *gqlClient, args.AreaID)

		if err != nil {
			return nil, generated.GetAreaDetailsResponse{}, fmt.Errorf("looking up area: %w", err)
		}
		return nil, *area, nil
	}
}
