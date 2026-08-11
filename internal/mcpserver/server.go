// Package mcpserver exposes the OpenBeta query layer as MCP tools.
//
// The tool and handler logic here is deliberately independent of the transport
// (NFR-9): New returns a configured *mcp.Server, and the caller decides whether
// to run it over stdio or something else.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
	"github.com/jacKlinc/openbeta-mcp/internal/tools"
)

// GetAreaDetailsArgs is the input schema for get_area_details.
type GetAreaDetailsArgs struct {
	AreaID string `json:"areaId" jsonschema:"The area's UUID, in 8-4-4-4-12 hex form. Obtain one from a crags_within result or from an earlier get_area_details children list."`
}

// New builds a server with both tools registered against client.
func New(client *openbeta.Client, version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "openbeta",
		Title:   "OpenBeta climbing areas",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "Query the OpenBeta rock climbing database for climbing areas and routes. " +
			"Climbing data is user-contributed: grades, protection and access notes are opinions " +
			"rather than facts, and may be wrong or out of date. When relaying anything " +
			"safety-critical, say that it should be verified against a current local guidebook.",
	})

	// Endpoint and HTTP client both come from client, so WithEndpoint reaches the query
	gqlClient := graphql.NewClient(client.Endpoint(), client.HTTPClient())

	mcp.AddTool(server, &mcp.Tool{
		Name: "crags_within",
		Description: "Find rock climbing areas inside a geographic bounding box. " +
			"Returns each crag's name, coordinates, climb count and location in the area " +
			"hierarchy, sorted with the largest crags first. Areas holding no climbs are " +
			"omitted. Use this to answer 'what can I climb near here', then pass a returned " +
			"uuid to get_area_details for the routes.",
	}, tools.HandleCragsWithin(&gqlClient))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_area_details",
		Description: "Get detail for one climbing area by UUID: name, coordinates, description, " +
			"and either its routes or its sub-areas. " +
			"An area has routes or sub-areas, never both — large areas return sub-areas, and " +
			"you descend through them to reach the routes. An empty climbs list with a " +
			"populated children list means the routes are one level down, not that the area " +
			"is empty.",
	}, handleGetAreaDetails(&gqlClient))

	return server
}

func handleGetAreaDetails(gqlClient *graphql.Client) mcp.ToolHandlerFor[GetAreaDetailsArgs, generated.GetAreaDetailsResponse] {
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
