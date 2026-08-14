// Package mcpserver exposes the OpenBeta query layer as MCP tools.
//
// The tool and handler logic here is deliberately independent of the transport
// (NFR-9): New returns a configured *mcp.Server, and the caller decides whether
// to run it over stdio or something else.
package mcpserver

import (
	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/geo"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/tools"
)

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
		Name: "crags_near",
		Description: "Find rock climbing areas near a place. Give a well-known climbing " +
			"destination or town as 'place' (for example Squamish, Bishop, Fontainebleau); " +
			"if the place is not recognised the error will say so, and you can retry with " +
			"'lnglat' as [longitude, latitude]. Returns each crag's name, coordinates, " +
			"distance and climb count, largest first, and only crags that actually hold " +
			"climbs. Use this to answer 'what can I climb near here', then pass a returned " +
			"uuid to get_area_details for the routes.",
	}, tools.HandleCragsNear(&gqlClient, geo.NewGazetteer()))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_area_details",
		Description: "Get detail for one climbing area by UUID: name, coordinates, description, " +
			"and either its routes or its sub-areas. " +
			"An area has routes or sub-areas, never both — large areas return sub-areas, and " +
			"you descend through them to reach the routes. An empty climbs list with a " +
			"populated children list means the routes are one level down, not that the area " +
			"is empty.",
	}, tools.HandleGetAreaDetails(&gqlClient))

	return server
}
