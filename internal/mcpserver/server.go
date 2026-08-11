// Package mcpserver exposes the OpenBeta query layer as MCP tools.
//
// The tool and handler logic here is deliberately independent of the transport
// (NFR-9): New returns a configured *mcp.Server, and the caller decides whether
// to run it over stdio or something else.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Khan/genqlient/graphql"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// defaultZoom returns individual crags rather than parent regions.
//
// cragsWithin switches hierarchy level at zoom 11: below it the API returns
// organizational parents ("Squamish"), at 11 and above individual crags
// ("Tantalus Wall"). 13 sits clear of that boundary, and "what can I climb
// here" almost always wants crags.
const defaultZoom = 13

// CragsWithinArgs is the input schema for crags_within.
//
// The bbox description carries the element ordering because the upstream schema
// types it as a bare [Float] — nothing validates the order server-side, and a
// transposed pair is the easiest mistake for a caller to make (NFR-12).
type CragsWithinArgs struct {
	BBox []float64 `json:"bbox" jsonschema:"Bounding box as exactly four numbers in the order [minLng, minLat, maxLng, maxLat]. Longitude comes first. Example for Squamish, BC: [-123.2, 49.6, -122.9, 49.8]"`
	Zoom *float64  `json:"zoom,omitempty" jsonschema:"Map zoom level controlling which level of the area hierarchy is returned. 11 or above returns individual crags; below 11 returns larger parent regions. Defaults to 13."`
}

// GetAreaDetailsArgs is the input schema for get_area_details.
type GetAreaDetailsArgs struct {
	AreaID string `json:"areaId" jsonschema:"The area's UUID, in 8-4-4-4-12 hex form. Obtain one from a crags_within result or from an earlier get_area_details children list."`
}

// CragsWithinResult is the output schema for crags_within.
type CragsWithinResult struct {
	Crags []openbeta.CragSummary `json:"crags"`
	Count int                    `json:"count"`
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

	mcp.AddTool(server, &mcp.Tool{
		Name: "crags_within",
		Description: "Find rock climbing areas inside a geographic bounding box. " +
			"Returns each crag's name, coordinates, climb count and location in the area " +
			"hierarchy, sorted with the largest crags first. Areas holding no climbs are " +
			"omitted. Use this to answer 'what can I climb near here', then pass a returned " +
			"uuid to get_area_details for the routes.",
	}, handleCragsWithin(client))

	mcp.AddTool(server, &mcp.Tool{
		Name: "get_area_details",
		Description: "Get detail for one climbing area by UUID: name, coordinates, description, " +
			"and either its routes or its sub-areas. " +
			"An area has routes or sub-areas, never both — large areas return sub-areas, and " +
			"you descend through them to reach the routes. An empty climbs list with a " +
			"populated children list means the routes are one level down, not that the area " +
			"is empty.",
	}, handleGetAreaDetails(client))

	return server
}

func handleCragsWithin(client *openbeta.Client) mcp.ToolHandlerFor[CragsWithinArgs, generated.CragsWithinResponse] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args CragsWithinArgs) (*mcp.CallToolResult, generated.CragsWithinResponse, error) {
		bbox, err := openbeta.NewBBox(args.BBox)
		if err != nil {
			return nil, generated.CragsWithinResponse{}, err
		}

		zoom := float64(defaultZoom)
		if args.Zoom != nil {
			zoom = *args.Zoom
		}
		filter := generated.SearchWithinFilter{Bbox: bbox[:], Zoom: zoom}
		// TODO: pass graphql cleint to function
		client := graphql.NewClient(openbeta.DefaultEndpoint, http.DefaultClient)
		crags, err := generated.CragsWithin(ctx, client, filter)
		if err != nil {
			return nil, generated.CragsWithinResponse{}, fmt.Errorf("looking up crags: %w", err)
		}

		return nil, *crags, nil
	}
}

func handleGetAreaDetails(client *openbeta.Client) mcp.ToolHandlerFor[GetAreaDetailsArgs, generated.GetAreaDetailsResponse] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, args GetAreaDetailsArgs) (*mcp.CallToolResult, generated.GetAreaDetailsResponse, error) {
		client := graphql.NewClient(openbeta.DefaultEndpoint, http.DefaultClient)

		area, err := generated.GetAreaDetails(ctx, client, args.AreaID)
		if err != nil {
			return nil, generated.GetAreaDetailsResponse{}, err
		}

		if err != nil {
			return nil, generated.GetAreaDetailsResponse{}, fmt.Errorf("looking up area: %w", err)
		}
		return nil, *area, nil
	}
}
