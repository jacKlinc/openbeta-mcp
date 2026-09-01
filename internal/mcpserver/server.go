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
			"climbs. " +
			"'count' is how many crags the radius found before the nearest 20 were kept, so a " +
			"count well above 'returned' means a narrower radius would show more of them. " +
			"Areas are not filtered by discipline: a crag listed here may be a boulder field " +
			"or an ice venue, and find_climbs returns roped rock routes only, so it can hold " +
			"nothing find_climbs will return. " +
			"Use this to answer 'what can I climb near here', then pass a returned " +
			"uuid to get_area_details for the routes.",
	}, tools.HandleCragsNear(gqlClient, geo.NewGazetteer()))

	mcp.AddTool(server, &mcp.Tool{
		Name: "find_climbs",
		Description: "Find individual roped rock routes near a place, filtered by grade, " +
			"discipline and whether they are multi-pitch. Takes the same 'place' or 'lnglat' " +
			"origin as crags_near. " +
			"Grades are never converted between systems. Write minGrade and maxGrade in the " +
			"system the crags being searched use — YDS in North America, French for sport " +
			"across most of Europe, UIAA mainly for trad and alpine routes, chiefly in " +
			"central Europe. The two coexist: an alpine route may carry both, such as the " +
			"Walker Spur at UIAA IV and French 6a. A crag's system is whichever OpenBeta " +
			"records for it, so check 'gradeSystem' on a result rather than assuming one " +
			"from the country. A crag your bounds cannot be written in is skipped and its " +
			"routes counted in 'skipped', so searching near Siurana for '5.10a' returns " +
			"nothing rather than an error; British crags are never reachable, because OpenBeta " +
			"records no British grades at all. Each route names its system in 'gradeSystem', " +
			"since one radius can span two. Bounds are inclusive at the edges, so a route " +
			"recorded imprecisely as '5.10' is returned for a 5.8 to 5.10b search. " +
			"'disciplines' defaults to all of sport, trad, alpine, aid and top rope; boulder " +
			"problems, deep water solos, ice, mixed and snow are never returned. " +
			"The API stores no pitch count, so 'multipitch' is inferred from route length: " +
			"'unknown' means the length was never recorded, not that the route is single " +
			"pitch. " +
			"The search stops once 30 routes have matched, so 'count' is a floor rather than a " +
			"total and crags further out may hold more; 'cragsScanned' says how far it got, so " +
			"no results with a non-zero scan means the crags searched genuinely hold nothing " +
			"matching.",
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
