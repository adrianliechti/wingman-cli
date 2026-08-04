package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

type ElicitFunc func(ctx context.Context, req tool.ElicitRequest) (tool.ElicitResult, error)

// SetElicit routes elicitation/create requests from connected MCP servers to
// the given UI surface. A nil handler declines all requests.
func (m *Manager) SetElicit(fn ElicitFunc) {
	if fn == nil {
		m.elicit.Store(nil)
		return
	}
	m.elicit.Store(&fn)
}

func (m *Manager) handleElicitation(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	fn := m.elicit.Load()
	if fn == nil {
		return &mcp.ElicitResult{Action: "decline"}, nil
	}

	// The transport context has no deadline; an undisplayable or forgotten
	// prompt must not hang the MCP session forever.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	elicitReq, err := convertElicitParams(req.Params)
	if err != nil {
		return nil, err
	}

	result, err := (*fn)(ctx, elicitReq)
	if err != nil {
		return nil, err
	}

	out := &mcp.ElicitResult{Action: string(result.Action)}
	if out.Action == "" {
		out.Action = "cancel"
	}
	if result.Action == tool.ElicitAccept {
		out.Content = coerceContent(result.Content, elicitReq.Fields)
	}

	return out, nil
}

func convertElicitParams(p *mcp.ElicitParams) (tool.ElicitRequest, error) {
	if p == nil {
		return tool.ElicitRequest{}, fmt.Errorf("missing elicitation params")
	}

	req := tool.ElicitRequest{Message: p.Message}

	if p.URL != "" {
		req.Message = strings.TrimSpace(p.Message + "\n\nOpen this URL to continue: " + p.URL)
		return req, nil
	}

	if p.RequestedSchema == nil {
		return req, nil
	}

	raw, err := json.Marshal(p.RequestedSchema)
	if err != nil {
		return tool.ElicitRequest{}, fmt.Errorf("invalid elicitation schema: %w", err)
	}

	var schema struct {
		Properties map[string]struct {
			Type        string         `json:"type"`
			Title       string         `json:"title"`
			Description string         `json:"description"`
			Enum        []any          `json:"enum"`
			EnumNames   []string       `json:"enumNames"`
			Default     any            `json:"default"`
			Meta        map[string]any `json:"_meta"`
		} `json:"properties"`
		Required []string `json:"required"`
	}

	if err := json.Unmarshal(raw, &schema); err != nil {
		return tool.ElicitRequest{}, fmt.Errorf("invalid elicitation schema: %w", err)
	}

	required := make(map[string]bool, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = true
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	slices.SortStableFunc(names, func(a, b string) int {
		switch {
		case required[a] == required[b]:
			return 0
		case required[a]:
			return -1
		default:
			return 1
		}
	})

	for _, name := range names {
		prop := schema.Properties[name]

		fieldType := prop.Type
		if fieldType == "" {
			fieldType = "string"
		}

		field := tool.ElicitField{
			Name:        name,
			Type:        fieldType,
			Title:       prop.Title,
			Description: prop.Description,
			Required:    required[name],
			Default:     prop.Default,
		}
		if marker, _ := prop.Meta["_askUserQuestionCustomAnswer"].(map[string]any); marker != nil {
			isCustom, _ := marker["isCustomAnswer"].(bool)
			if isCustom {
				field.CustomAnswerFor, _ = marker["questionId"].(string)
			}
		}

		for _, value := range prop.Enum {
			field.Enum = append(field.Enum, formatEnumValue(value))
		}
		if len(field.Enum) > 0 {
			field.Strict = true
		}
		if len(prop.EnumNames) == len(field.Enum) {
			field.EnumDescriptions = prop.EnumNames
		}

		req.Fields = append(req.Fields, field)
	}

	return req, nil
}

// formatEnumValue renders an enum member for display and round-tripping.
// JSON numbers decode as float64, and %v would print large integers in
// scientific notation ("1e+06"), which no longer parses back into the enum.
func formatEnumValue(value any) string {
	if f, ok := value.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", value)
}

// coerceContent aligns UI-submitted values with the field types the server
// declared, since form surfaces deliver everything as strings.
func coerceContent(content map[string]any, fields []tool.ElicitField) map[string]any {
	if len(content) == 0 {
		return content
	}

	types := make(map[string]string, len(fields))
	for _, f := range fields {
		types[f.Name] = f.Type
	}

	out := make(map[string]any, len(content))
	for key, value := range content {
		str, isString := value.(string)
		if !isString {
			out[key] = value
			continue
		}

		switch types[key] {
		case "boolean":
			if b, err := strconv.ParseBool(strings.TrimSpace(str)); err == nil {
				out[key] = b
				continue
			}
		case "integer":
			if n, err := strconv.ParseInt(strings.TrimSpace(str), 10, 64); err == nil {
				out[key] = n
				continue
			}
		case "number":
			if f, err := strconv.ParseFloat(strings.TrimSpace(str), 64); err == nil {
				out[key] = f
				continue
			}
		}

		out[key] = str
	}

	return out
}
