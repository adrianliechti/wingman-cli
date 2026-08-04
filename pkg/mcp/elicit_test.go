package mcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestConvertElicitParams(t *testing.T) {
	req, err := convertElicitParams(&mcp.ElicitParams{
		Message: "Configure the export",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{
					"type": "integer",
					"enum": []any{float64(1000000), float64(2000000)},
				},
				"note": map[string]any{
					"type":    "string",
					"default": "none",
					"_meta": map[string]any{"_askUserQuestionCustomAnswer": map[string]any{
						"questionId": "limit", "isCustomAnswer": true,
					}},
				},
			},
			"required": []any{"limit"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(req.Fields) != 2 {
		t.Fatalf("fields = %+v", req.Fields)
	}

	limit := req.Fields[0]
	if limit.Name != "limit" || !limit.Required {
		t.Fatalf("required field must sort first, got %+v", req.Fields)
	}
	if !limit.Strict {
		t.Error("MCP enum fields must be strict (closed set)")
	}
	if len(limit.Enum) != 2 || limit.Enum[0] != "1000000" || limit.Enum[1] != "2000000" {
		t.Fatalf("integer enum must not render in scientific notation: %v", limit.Enum)
	}

	note := req.Fields[1]
	if note.Strict || note.Default != "none" || note.CustomAnswerFor != "limit" {
		t.Fatalf("note field = %+v", note)
	}
}

func TestConvertElicitParamsURLMode(t *testing.T) {
	req, err := convertElicitParams(&mcp.ElicitParams{
		Message: "Sign in to continue",
		URL:     "https://example.com/auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Fields) != 0 {
		t.Fatalf("url elicitation must have no fields, got %+v", req.Fields)
	}
	if !strings.Contains(req.Message, "https://example.com/auth") {
		t.Fatalf("message must carry the URL: %q", req.Message)
	}
}

func TestCoerceContent(t *testing.T) {
	fields := []tool.ElicitField{
		{Name: "count", Type: "integer"},
		{Name: "ratio", Type: "number"},
		{Name: "ok", Type: "boolean"},
		{Name: "name", Type: "string"},
	}

	out := coerceContent(map[string]any{
		"count": "1000000",
		"ratio": "2.5",
		"ok":    "true",
		"name":  "x",
	}, fields)

	if v, ok := out["count"].(int64); !ok || v != 1000000 {
		t.Errorf("count = %#v", out["count"])
	}
	if v, ok := out["ratio"].(float64); !ok || v != 2.5 {
		t.Errorf("ratio = %#v", out["ratio"])
	}
	if v, ok := out["ok"].(bool); !ok || !v {
		t.Errorf("ok = %#v", out["ok"])
	}
	if v, ok := out["name"].(string); !ok || v != "x" {
		t.Errorf("name = %#v", out["name"])
	}
}
