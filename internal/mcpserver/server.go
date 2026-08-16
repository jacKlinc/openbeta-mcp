// Package mcpserver exposes the OpenBeta query layer as MCP tools.
//
// The tool and handler logic here is deliberately independent of the transport
// (NFR-9): New returns a configured *mcp.Server, and the caller decides whether
// to run it over stdio or something else.
package mcpserver

import (
	"context"
	"sync/atomic"
	"time"

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
	}, tools.HandleCragsNear(gqlClient, geo.NewGazetteer()))

	mcp.AddTool(server, &mcp.Tool{
		Name: "find_climbs",
		Description: "Find individual trad routes near a place, filtered by YDS grade and by " +
			"whether they are multi-pitch. Takes the same 'place' or 'lnglat' origin as " +
			"crags_near. minGrade and maxGrade are inclusive at the edges, so a route " +
			"recorded imprecisely as '5.10' is returned for a 5.8 to 5.10b search. " +
			"Currently trad only, and YDS only — sport routes and boulder problems are " +
			"not returned. " +
			"The API stores no pitch count, so 'multipitch' is inferred from route length: " +
			"'unknown' means the length was never recorded, not that the route is single " +
			"pitch. 'cragsScanned' tells you how many crags were searched, so no results " +
			"with a non-zero scan means the area genuinely holds nothing matching.",
	}, tools.HandleFindClimbs(gqlClient, geo.NewGazetteer()))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_area_details",
		Description: "Get detail for one climbing area by UUID: name, coordinates, description, " +
			"and either its routes or its sub-areas. " +
			"An area has routes or sub-areas, never both — large areas return sub-areas, and " +
			"you descend through them to reach the routes. An empty climbs list with a " +
			"populated children list means the routes are one level down, not that the area " +
			"is empty.",
	}, tools.HandleGetAreaDetails(gqlClient))

	// Count number of round trips each tool makes
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// CallToolParamsRaw allows metadata to be added to the responses
			toolParams, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok {
				return next(ctx, method, req)
			}
			var n atomic.Int32
			start := time.Now()
			res, callErr := next(openbeta.WithCounter(ctx, &n), method, req)
			// Read before the sink runs, so its syscalls stay out of the sample.
			elapsed := time.Since(start)

			// A tool that rejects its input reports the failure in the result,
			// not as a Go error, so callErr alone recorded every validation
			// rejection as a success.
			failed := callErr != nil
			if out, ok := res.(*mcp.CallToolResult); ok && out.IsError {
				failed = true
			}

			recordCall(toolParams.Name, toolParams.Arguments, start, elapsed, n.Load(), failed)
			return res, callErr
		}
	})
	return server
}
