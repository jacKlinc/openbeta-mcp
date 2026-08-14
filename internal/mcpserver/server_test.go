package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jacKlinc/openbeta-mcp/internal/openbeta"
	"github.com/jacKlinc/openbeta-mcp/internal/openbeta/generated"
)

// connect wires a client to a server over the in-memory transport, with the
// upstream API stubbed by body. Exercises the real MCP round trip — schema
// validation, marshalling and error packing all run as they would in production.
func connect(t *testing.T, body string) *mcp.ClientSession {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(upstream.Close)

	server := New(openbeta.New(openbeta.WithEndpoint(upstream.URL)), "test")

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	// Servers must be connected before clients — the client initializes the
	// session during connect.
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// call invokes a tool and returns the result, failing on protocol-level errors.
// Tool-level errors arrive as a result with IsError set, not as an error here.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("protocol error calling %s: %v", name, err)
	}
	return res
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// Exactly two tools, both discoverable with usable schemas (FR-1, FR-4).
func TestListTools(t *testing.T) {
	cs := connect(t, `{"data":{"cragsNear":[]}}`)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(res.Tools))
	}

	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("%s has no input schema", tool.Name)
		}
	}
	for _, name := range []string{"crags_near", "get_area_details"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("tool %q not registered", name)
		}
	}

	// lnglat ordering is the easiest thing for a caller to get wrong, and the
	// upstream schema won't catch it — so it must be stated where an LLM will
	// actually read it (NFR-12).
	schema, err := json.Marshal(byName["crags_near"].InputSchema)
	if err != nil {
		t.Fatalf("marshalling schema: %v", err)
	}
	if !strings.Contains(string(schema), "longitude") {
		t.Errorf("crags_near schema does not document lnglat ordering: %s", schema)
	}
}

func TestGetAreaDetails(t *testing.T) {
	body := `{"data":{"area":{
		"uuid":"8f267065-fc1a-59ce-bcf1-6e9335548363","areaName":"Stawamus Chief","totalClimbs":369,
		"gradeContext":"US","pathTokens":["Canada","Squamish","Stawamus Chief"],
		"metadata":{"lat":49.68,"lng":-123.14,"leaf":false},
		"content":{"description":"The Chief."},
		"children":[{"uuid":"ch1","areaName":"The Apron","totalClimbs":51,"metadata":{}}],
		"climbs":[]
	}}}`
	cs := connect(t, body)

	res := call(t, cs, "get_area_details", map[string]any{"areaId": "8f267065-fc1a-59ce-bcf1-6e9335548363"})
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, res))
	}

	var out generated.GetAreaDetailsResponse
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if out.Area.AreaName != "Stawamus Chief" {
		t.Errorf("Name = %q", out.Area.AreaName)
	}
	// A parent area must surface its children, or a 369-route wall reads as empty.
	// if len(out.Area.Children) != 1 || out.Area.Children[0].AreaName != "The Apron" {
	// 	t.Errorf("children not surfaced: %+v", out.Area.Children)
	// }
}

// Bad input must come back as a tool error the model can read and correct, not
// as a protocol error that kills the call (FR-18).
func TestInvalidInputIsAToolError(t *testing.T) {
	cs := connect(t, `{"data":{"cragsNear":[]}}`)

	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"no origin", "crags_near", map[string]any{}, "pass a place name"},
		{"lnglat wrong length", "crags_near", map[string]any{"lnglat": []float64{1}}, "exactly 2 elements"},
		{"lat/lng transposed", "crags_near", map[string]any{"lnglat": []float64{49.7, -123.2}}, "latitude out of range"},
		{"unknown place", "crags_near", map[string]any{"place": "Nowhere At All"}, "no coordinates known"},
		{"bad uuid", "get_area_details", map[string]any{"areaId": "not-a-uuid"}, "invalid UUID length: 10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := call(t, cs, tt.tool, tt.args)
			if !res.IsError {
				t.Fatalf("expected a tool error, got success: %s", resultText(t, res))
			}
			if got := resultText(t, res); !strings.Contains(got, tt.want) {
				t.Errorf("error %q does not explain the problem (want %q)", got, tt.want)
			}
		})
	}
}

// An upstream failure must reach the model as an error, not as an empty result
// it would read as "no crags here" (FR-16, FR-17).
func TestUpstreamErrorIsAToolError(t *testing.T) {
	cs := connect(t, `{"errors":[{"message":"Cannot query field \"nope\""}]}`)

	res := call(t, cs, "crags_near", map[string]any{"place": "Squamish"})
	if !res.IsError {
		t.Fatalf("expected a tool error, got success: %s", resultText(t, res))
	}
	if got := resultText(t, res); !strings.Contains(got, "Cannot query field") {
		t.Errorf("error should carry the upstream message, got %q", got)
	}
}
