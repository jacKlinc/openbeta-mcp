package tools

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

type GetAreaDetailsArgs struct {
	AreaID string `json:"areaId" jsonschema:"The area's UUID, in 8-4-4-4-12 hex form. Obtain one from a crags_within result or from an earlier get_area_details children list."`
}

func HandleGetAreaDetails(gql graphql.Client) mcp.ToolHandlerFor[GetAreaDetailsArgs, generated.GetAreaDetailsResponse] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args GetAreaDetailsArgs) (*mcp.CallToolResult, generated.GetAreaDetailsResponse, error) {
		out, err := getAreaDetails(ctx, gql, args)
		return nil, out, err
	}
}

func getAreaDetails(ctx context.Context, gql graphql.Client, args GetAreaDetailsArgs) (generated.GetAreaDetailsResponse, error) {
	// Validated here rather than upstream: the API answers a malformed UUID
	// with "area Invalid UUID.", which does not tell a model what to fix.
	//
	// uuid.Parse alone is too lenient — it accepts the dashless 32-hex form,
	// urn:uuid: prefixes and brace-wrapped values, none of which the API
	// takes. Every UUID a caller gets from crags_within is canonical, so
	// require that form and reject the rest here.
	if _, err := uuid.Parse(args.AreaID); err != nil {
		return generated.GetAreaDetailsResponse{}, err
	}
	s := args.AreaID
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return generated.GetAreaDetailsResponse{}, errors.New("uuid: missing required hyphens")
	}

	area, err := generated.GetAreaDetails(ctx, gql, args.AreaID)

	if err != nil {
		return generated.GetAreaDetailsResponse{}, err
	}
	return *area, nil
}
