package elicit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	. "github.com/adrianliechti/wingman-agent/pkg/agent/tool/elicit"
)

func answering(content map[string]any, capture *tool.ElicitRequest) *tool.Elicitation {
	return &tool.Elicitation{
		Elicit: func(_ context.Context, req tool.ElicitRequest) (tool.ElicitResult, error) {
			if capture != nil {
				*capture = req
			}
			return tool.ElicitResult{Action: tool.ElicitAccept, Content: content}, nil
		},
	}
}

func question(q, header string, options ...map[string]any) map[string]any {
	entry := map[string]any{"question": q, "header": header}
	if len(options) > 0 {
		items := make([]any, len(options))
		for i, o := range options {
			items[i] = o
		}
		entry["options"] = items
	}
	return entry
}

func TestToolsReturnsNilWithoutElicitation(t *testing.T) {
	if got := Tools(nil); got != nil {
		t.Fatalf("Tools(nil) = %v, want nil", got)
	}

	if got := Tools(&tool.Elicitation{}); got != nil {
		t.Fatalf("Tools with nil Elicit = %v, want nil", got)
	}
}

func TestToolsReturnsHiddenElicitTool(t *testing.T) {
	tools := Tools(answering(nil, nil))
	if len(tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(tools))
	}

	et := tools[0]
	if et.Name != "elicit" {
		t.Errorf("name = %q, want elicit", et.Name)
	}
	if !et.Hidden {
		t.Error("elicit must be Hidden so subagents never receive it")
	}
	if et.Effect == nil || et.Effect(nil) != tool.EffectReadOnly {
		t.Error("elicit effect must classify as EffectReadOnly")
	}

	required, ok := et.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "questions" {
		t.Errorf("required = %v, want [questions]", et.Parameters["required"])
	}
}

func TestExecuteAllowsHeaderlessSingleQuestion(t *testing.T) {
	var captured tool.ElicitRequest
	et := Tools(answering(map[string]any{"q_1": "blue"}, &captured))[0]

	answer, err := et.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{"question": "Favorite color?"}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if answer.Content != "blue" {
		t.Fatalf("answer = %q, want blue", answer.Content)
	}

	if len(captured.Fields) != 1 || captured.Fields[0].Name != "q_1" {
		t.Fatalf("fields = %+v, want single q_1", captured.Fields)
	}
	if captured.Message != "Favorite color?" {
		t.Fatalf("message = %q", captured.Message)
	}
}

func TestExecuteValidatesQuestions(t *testing.T) {
	et := Tools(answering(nil, nil))[0]

	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing", map[string]any{}},
		{"empty", map[string]any{"questions": []any{}}},
		{"no question", map[string]any{"questions": []any{map[string]any{"header": "X"}}}},
		{"option without label", map[string]any{"questions": []any{
			question("Which?", "Pick", map[string]any{"description": "no label"}),
		}}},
		{"too many", map[string]any{"questions": []any{
			question("1?", "A"), question("2?", "B"), question("3?", "C"),
			question("4?", "D"), question("5?", "E"),
		}}},
	}

	for _, tc := range cases {
		if _, err := et.Execute(context.Background(), tc.args); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestExecuteSingleQuestionHoistsMessage(t *testing.T) {
	var got tool.ElicitRequest
	et := Tools(answering(map[string]any{"database": "Postgres"}, &got))[0]

	result, err := et.Execute(context.Background(), map[string]any{
		"questions": []any{question("Which database?", "Database",
			map[string]any{"label": "Postgres", "description": "relational", "preview": "CREATE TABLE …"},
			map[string]any{"label": "Redis", "description": "key-value"},
		)},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "Postgres" {
		t.Errorf("result = %q, want Postgres", result.Content)
	}

	if got.Message != "Which database?" {
		t.Errorf("message = %q", got.Message)
	}
	if len(got.Fields) != 1 {
		t.Fatalf("fields = %+v", got.Fields)
	}
	f := got.Fields[0]
	if f.Name != "database" || f.Title != "Database" || f.Description != "" {
		t.Errorf("field = %+v", f)
	}
	if len(f.Enum) != 2 || f.Enum[0] != "Postgres" {
		t.Errorf("enum = %v", f.Enum)
	}
	if len(f.EnumDescriptions) != 2 || f.EnumDescriptions[1] != "key-value" {
		t.Errorf("descriptions = %v", f.EnumDescriptions)
	}
	if len(f.EnumPreviews) != 2 || f.EnumPreviews[0] != "CREATE TABLE …" {
		t.Errorf("previews = %v", f.EnumPreviews)
	}
}

func TestExecuteMultipleQuestionsAndMultiSelect(t *testing.T) {
	var got tool.ElicitRequest
	et := Tools(answering(map[string]any{
		"scope":    []any{"API", "CLI"},
		"language": "Go",
	}, &got))[0]

	multi := question("Which parts?", "Scope",
		map[string]any{"label": "API", "description": "server"},
		map[string]any{"label": "CLI", "description": "terminal"},
	)
	multi["multi_select"] = true

	result, err := et.Execute(context.Background(), map[string]any{
		"questions": []any{
			multi,
			question("Which language?", "Language"),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Fields) != 2 {
		t.Fatalf("fields = %+v", got.Fields)
	}
	if !got.Fields[0].Multiple {
		t.Error("multi_select must map to Multiple")
	}
	if got.Fields[1].Description != "Which language?" {
		t.Errorf("multi-question fields keep their question text, got %+v", got.Fields[1])
	}
	if got.Message != "" {
		t.Errorf("multi-question requests have no top-level message, got %q", got.Message)
	}

	if !strings.Contains(result.Content, "Scope: API, CLI") || !strings.Contains(result.Content, "Language: Go") {
		t.Errorf("rendered answers = %q", result.Content)
	}
}

func TestExecuteRendersDeclineAndCancel(t *testing.T) {
	for action, want := range map[tool.ElicitAction]string{
		tool.ElicitDecline: "declined",
		tool.ElicitCancel:  "dismissed",
	} {
		elicitation := &tool.Elicitation{
			Elicit: func(context.Context, tool.ElicitRequest) (tool.ElicitResult, error) {
				return tool.ElicitResult{Action: action}, nil
			},
		}
		et := Tools(elicitation)[0]

		result, err := et.Execute(context.Background(), map[string]any{
			"questions": []any{question("?", "Q")},
		})
		if err != nil {
			t.Fatalf("action %s: unexpected error: %v", action, err)
		}
		if !strings.Contains(strings.ToLower(result.Content), want) {
			t.Errorf("action %s: result %q should mention %q", action, result.Content, want)
		}
	}
}

func TestExecutePropagatesElicitationError(t *testing.T) {
	sentinel := errors.New("transport down")
	elicitation := &tool.Elicitation{
		Elicit: func(context.Context, tool.ElicitRequest) (tool.ElicitResult, error) {
			return tool.ElicitResult{}, sentinel
		},
	}
	et := Tools(elicitation)[0]

	_, err := et.Execute(context.Background(), map[string]any{
		"questions": []any{question("?", "Q")},
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error to propagate, got: %v", err)
	}
}
