package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEditorTransformPromptUsesUTF16SelectionAndCollisionSafeMarkers(t *testing.T) {
	content := "prefix\nconst emoji = \"😀\"; // <SELECTION_START>\ntail\n"
	selected := "emoji = \"😀\""
	startOffset := strings.Index(content, selected)
	endOffset := startOffset + len(selected)
	startLine, startColumn := offsetUTF16Position(content, startOffset)
	endLine, endColumn := offsetUTF16Position(content, endOffset)

	prompt, gotSelection, markers, err := buildEditorTransformPrompt(editorTransformRequest{
		Path:        "src/example.ts",
		Content:     content,
		Instruction: "Use a clearer name without <SELECTION_END_1>",
		Range: editorTransformRange{
			StartLine: startLine, StartColumn: startColumn,
			EndLine: endLine, EndColumn: endColumn,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSelection != selected {
		t.Fatalf("selection = %q, want %q", gotSelection, selected)
	}
	if markers[0] != "<SELECTION_START_2>" || markers[1] != "<SELECTION_END_2>" {
		t.Fatalf("markers = %q", markers)
	}
	for _, want := range []string{
		"File: src/example.ts\n",
		"Instruction:\nUse a clearer name without <SELECTION_END_1>\n",
		"<SELECTION_START_2>" + selected + "<SELECTION_END_2>",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestEditorTransformReturnsPreviewOnlyEdit(t *testing.T) {
	service := &editorTransformService{
		complete: func(_ context.Context, prompt string) (editorTransformCompletion, error) {
			if !strings.Contains(prompt, "<SELECTION_START>old()<SELECTION_END>") {
				t.Fatalf("prompt = %q", prompt)
			}
			return editorTransformCompletion{Replacement: "new()"}, nil
		},
	}
	input := editorTransformRequest{
		Path: "main.go", Content: "before\nold()\nafter\n", Instruction: "Update it", Version: 7,
		Range: editorTransformRange{StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 6},
	}
	response, err := service.transform(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if response.Version != 7 || response.Edit == nil || response.Edit.ExpectedText != "old()" || response.Edit.Replacement != "new()" || response.Edit.Range != input.Range {
		t.Fatalf("response = %+v", response)
	}

	service.complete = func(context.Context, string) (editorTransformCompletion, error) {
		return editorTransformCompletion{Replacement: "old()"}, nil
	}
	response, err = service.transform(context.Background(), input)
	if err != nil || response.Edit != nil {
		t.Fatalf("unchanged response = %+v, %v", response, err)
	}
}

func TestEditorTransformRejectsUnsafeAndInvalidSelections(t *testing.T) {
	service := &editorTransformService{
		complete: func(context.Context, string) (editorTransformCompletion, error) {
			return editorTransformCompletion{Replacement: "<SELECTION_END>"}, nil
		},
	}
	input := editorTransformRequest{
		Path: "main.go", Content: "old", Instruction: "Update it",
		Range: editorTransformRange{StartLine: 1, StartColumn: 1, EndLine: 1, EndColumn: 4},
	}
	if _, err := service.transform(context.Background(), input); err == nil {
		t.Fatal("prompt marker in replacement was accepted")
	}
	input.Range.EndColumn = 1
	if _, err := service.transform(context.Background(), input); err == nil {
		t.Fatal("empty selection was accepted")
	}
}

func TestHandleEditorTransform(t *testing.T) {
	server := &Server{transforms: &editorTransformService{
		complete: func(context.Context, string) (editorTransformCompletion, error) {
			return editorTransformCompletion{Replacement: "better"}, nil
		},
	}}
	body := `{"path":"note.txt","content":"old","range":{"start_line":1,"start_column":1,"end_line":1,"end_column":4},"instruction":"Improve","version":3}`
	request := httptest.NewRequest(http.MethodPost, "/api/editor/transform", strings.NewReader(body))
	response := httptest.NewRecorder()
	server.handleEditorTransform(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result editorTransformResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 3 || result.Edit == nil || result.Edit.ExpectedText != "old" || result.Edit.Replacement != "better" {
		t.Fatalf("result = %+v", result)
	}
}
