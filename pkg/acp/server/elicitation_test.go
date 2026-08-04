package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestElicitationSchemaPreservesEnumDescriptions(t *testing.T) {
	schema := elicitationSchema(tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name:             "strategy",
		Type:             "string",
		Title:            "Strategy",
		Description:      "Choose a strategy",
		Required:         true,
		Enum:             []string{"safe", "fast"},
		EnumDescriptions: []string{"Minimal changes", "Broader optimization"},
		Default:          "safe",
	}}})

	property, ok := schema.Properties["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("property = %#v", schema.Properties["strategy"])
	}
	choices, ok := property["oneOf"].([]map[string]any)
	if !ok || len(choices) != 2 {
		t.Fatalf("oneOf = %#v", property["oneOf"])
	}
	if choices[0]["const"] != "safe" || choices[0]["description"] != "Minimal changes" {
		t.Fatalf("first choice = %#v", choices[0])
	}
	if !reflect.DeepEqual(schema.Required, []string{"strategy"}) {
		t.Fatalf("required = %v", schema.Required)
	}
}

func TestElicitationSchemaUsesAnyOfForMultiSelect(t *testing.T) {
	schema := elicitationSchema(tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name:             "targets",
		Type:             "string",
		Multiple:         true,
		Enum:             []string{"tests", "docs"},
		EnumDescriptions: []string{"Run tests", "Update docs"},
	}}})

	property := schema.Properties["targets"].(map[string]any)
	if property["type"] != "array" {
		t.Fatalf("type = %v", property["type"])
	}
	items := property["items"].(map[string]any)
	if _, ok := items["anyOf"].([]map[string]any); !ok {
		t.Fatalf("items = %#v", items)
	}
}

func TestElicitationSchemaPreservesCustomAnswerCompanion(t *testing.T) {
	schema := elicitationSchema(tool.ElicitRequest{Fields: []tool.ElicitField{
		{Name: "question_0", Enum: []string{"Red", "Blue"}, Strict: true},
		{Name: "question_0_custom", CustomAnswerFor: "question_0"},
	}})

	property := schema.Properties["question_0_custom"].(map[string]any)
	meta := property["_meta"].(map[string]any)
	marker := meta["_askUserQuestionCustomAnswer"].(map[string]any)
	if marker["questionId"] != "question_0" || marker["isCustomAnswer"] != true {
		t.Fatalf("custom answer marker = %#v", marker)
	}
}

func TestElicitationFallbackUsesDefaults(t *testing.T) {
	result := elicitationFallback(tool.ElicitRequest{Fields: []tool.ElicitField{
		{Name: "mode", Required: true, Default: "safe"},
		{Name: "note"},
	}})
	if result.Action != tool.ElicitAccept || result.Content["mode"] != "safe" {
		t.Fatalf("result = %#v", result)
	}

	result = elicitationFallback(tool.ElicitRequest{Fields: []tool.ElicitField{{Name: "name", Required: true}}})
	if result.Action != tool.ElicitCancel {
		t.Fatalf("missing required value should cancel, got %#v", result)
	}
}

func TestElicitationCancelModeBypassesDefaults(t *testing.T) {
	t.Setenv("WINGMAN_ELICITATION", "cancel")

	result, err := (&Server{}).Elicit(context.Background(), tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name:    "mode",
		Default: "unsafe-default",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != tool.ElicitCancel {
		t.Fatalf("elicitation action = %q, want cancel", result.Action)
	}
}

func TestElicitationAcceptModeAnswersBooleanAndUsesDefaults(t *testing.T) {
	t.Setenv("WINGMAN_ELICITATION", "accept")

	result, err := (&Server{}).Elicit(context.Background(), tool.ElicitRequest{Fields: []tool.ElicitField{
		{Name: "proceed", Type: "boolean", Required: true},
		{Name: "mode", Type: "string", Required: true, Default: "safe"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != tool.ElicitAccept || result.Content["proceed"] != true || result.Content["mode"] != "safe" {
		t.Fatalf("elicitation result = %#v", result)
	}
}

func TestElicitationAcceptModeDoesNotInventRequiredText(t *testing.T) {
	t.Setenv("WINGMAN_ELICITATION", "accept")

	result, err := (&Server{}).Elicit(context.Background(), tool.ElicitRequest{Fields: []tool.ElicitField{{
		Name: "url", Type: "string", Required: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != tool.ElicitCancel {
		t.Fatalf("elicitation action = %q, want cancel", result.Action)
	}
}
