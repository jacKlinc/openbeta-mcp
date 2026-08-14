package tools

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

type FindAreaInput struct {
	Filter generated.Filter `json:"filter"`
	Limit  int              `json:"limit"`
}

func HandleFindArea(gql *graphql.Client) mcp.ToolHandlerFor[FindAreaInput, generated.FindAreaResponse] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args FindAreaInput) (*mcp.CallToolResult, generated.FindAreaResponse, error) {
		out, err := generated.FindArea(ctx, *gql, args.Filter, args.Limit)
		return nil, *out, err
	}
}
