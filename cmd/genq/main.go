package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/Khan/genqlient/graphql"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// Hello-world sanity check for the genqlient-generated client.
// Run with: go run ./cmd/genq
func main() {
	client := graphql.NewClient(openbeta.DefaultEndpoint, http.DefaultClient)

	// Stawamus Chief, Squamish
	const uuid = "8f267065-fc1a-59ce-bcf1-6e9335548363"

	resp, err := generated.GetArea(context.Background(), client, uuid)
	if err != nil {
		log.Fatal(err)
	}

	area := resp.Area
	fmt.Printf("%s (%v)\n", area.AreaName, area.PathTokens)
	for _, child := range area.Children {
		fmt.Printf("  - %s %s\n", child.AreaName, child.Uuid)
	}
}
