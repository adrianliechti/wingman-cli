package elicit

import (
	"context"
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

const maxQuestions = 4

func Tools(elicit *tool.Elicitation) []tool.Tool {
	if elicit == nil || elicit.Elicit == nil {
		return nil
	}

	description := strings.Join([]string{
		"Ask the user one or more questions and wait for their answers. Blocks until they reply.",
		"- Use only when blocked on a decision that is genuinely the user's — product intent, ambiguous requirements, a preference the workspace cannot answer. If being wrong is cheap to correct, assume and proceed instead.",
		"- 1-4 questions per call, usually with 2-4 mutually exclusive `options` with one-line tradeoff descriptions. Put a recommended option first, labeled \"(Recommended)\". When asking several questions, give each a short `header` chip (max ~12 chars) so answers stay attributable; omit `header` for a single question.",
		"- The UI always adds a free-text \"Other\" reply — never add such an option; options are shortcuts, not constraints. Omit `options` entirely for open-ended questions.",
		"- `multi_select` allows several picks (the answer becomes a list); `preview` (single-select only) renders code/mockups while the user compares options.",
		"- Not for tool-execution confirmations (handled by the harness).",
	}, "\n")

	return []tool.Tool{{
		Name:        "elicit",
		Description: description,
		Effect:      tool.StaticEffect(tool.EffectReadOnly),

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "The questions to ask the user (1-4).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"question": map[string]any{"type": "string", "description": "The full question text."},
							"header":   map[string]any{"type": "string", "description": "Chip label, e.g. \"Auth method\"; used to attribute answers when asking several questions. Omit for a single question."},
							"options": map[string]any{
								"type":        "array",
								"description": "The choices (2-4). Omit for a free-form question.",
								"items": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"label":       map[string]any{"type": "string", "description": "Concise display text (1-5 words); returned verbatim when chosen."},
										"description": map[string]any{"type": "string", "description": "What choosing this means; its tradeoff."},
										"preview":     map[string]any{"type": "string", "description": "Optional code/mockup shown while focused."},
									},
									"required":             []string{"label", "description"},
									"additionalProperties": false,
								},
							},
							"multi_select": map[string]any{"type": "boolean", "description": "Allow selecting multiple options.", "default": false},
						},
						"required":             []string{"question"},
						"additionalProperties": false,
					},
				},
			},

			"required":             []string{"questions"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			questions, ok := args["questions"].([]any)

			if !ok || len(questions) == 0 {
				return tool.Result{}, fmt.Errorf("questions is required")
			}
			if len(questions) > maxQuestions {
				return tool.Result{}, fmt.Errorf("at most %d questions per call; split the rest into a follow-up", maxQuestions)
			}

			fields, err := parseQuestions(questions)
			if err != nil {
				return tool.Result{}, err
			}

			req := tool.ElicitRequest{Fields: fields}
			if len(fields) == 1 {
				// A single question reads best as the prompt message itself.
				req.Message = fields[0].Description
				req.Fields[0].Description = ""
			}

			result, err := elicit.Elicit(ctx, req)

			if err != nil {
				return tool.Result{}, err
			}

			switch result.Action {
			case tool.ElicitAccept:
				return tool.Text(renderAnswers(fields, result.Content)), nil
			case tool.ElicitDecline:
				return tool.Text("The user declined to answer. Proceed without this input; do not re-ask the same questions."), nil
			default:
				return tool.Text("The user dismissed the questions without answering."), nil
			}
		},

		Hidden: true,
	}}
}

func parseQuestions(raw []any) ([]tool.ElicitField, error) {
	fields := make([]tool.ElicitField, 0, len(raw))
	seen := map[string]bool{}

	for i, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("questions[%d] must be an object", i)
		}

		question, _ := entry["question"].(string)
		if strings.TrimSpace(question) == "" {
			return nil, fmt.Errorf("questions[%d].question is required", i)
		}

		header, _ := entry["header"].(string)
		header = strings.TrimSpace(header)

		title := header
		if title == "" {
			title = truncateQuestion(question, 40)
		}

		name := slugify(header)
		if name == "" {
			name = fmt.Sprintf("q_%d", i+1)
		}
		if seen[name] {
			name = fmt.Sprintf("%s_%d", name, i+1)
		}
		seen[name] = true

		multiple, _ := entry["multi_select"].(bool)

		field := tool.ElicitField{
			Name:        name,
			Type:        "string",
			Title:       title,
			Description: strings.TrimSpace(question),
			Required:    true,
			Multiple:    multiple,
		}

		if rawOptions, ok := entry["options"].([]any); ok {
			for j, rawOption := range rawOptions {
				option, ok := rawOption.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("questions[%d].options[%d] must be an object with a label", i, j)
				}
				label, _ := option["label"].(string)
				if strings.TrimSpace(label) == "" {
					return nil, fmt.Errorf("questions[%d].options[%d].label is required", i, j)
				}
				optionDescription, _ := option["description"].(string)
				preview, _ := option["preview"].(string)

				field.Enum = append(field.Enum, strings.TrimSpace(label))
				field.EnumDescriptions = append(field.EnumDescriptions, strings.TrimSpace(optionDescription))
				field.EnumPreviews = append(field.EnumPreviews, preview)
			}
		}

		fields = append(fields, field)
	}

	return fields, nil
}

func renderAnswers(fields []tool.ElicitField, content map[string]any) string {
	if len(fields) == 1 {
		answer := answerText(content[fields[0].Name])
		if answer == "" {
			return "The user submitted an empty answer."
		}
		return answer
	}

	var b strings.Builder
	b.WriteString("User answers:")
	for _, field := range fields {
		answer := answerText(content[field.Name])
		if answer == "" {
			answer = "(no answer)"
		}
		fmt.Fprintf(&b, "\n- %s: %s", field.Title, answer)
	}
	return b.String()
}

func answerText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.TrimSpace(strings.Join(v, ", "))
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return strings.TrimSpace(strings.Join(parts, ", "))
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func truncateQuestion(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}

func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
