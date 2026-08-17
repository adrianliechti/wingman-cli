package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func TestEditorTabPromptProvidesBoundedEditContext(t *testing.T) {
	lines := make([]string, 25)
	for index := range lines {
		lines[index] = fmt.Sprintf("line %02d", index+1)
	}
	content := strings.Join(lines, "\n") + "\n"
	previous := strings.Replace(content, "line 12", "old  12", 1)
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "pkg/example.go",
		Content:         content,
		PreviousContent: previous,
		Line:            12,
		Column:          8,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"File: pkg/example.go\n",
		"Cursor: 12:8\n",
		"Cursor marker: <CURSOR>\n",
		"Edit window: lines 2-22\n",
		"<FILE_CONTEXT>\n",
		"<RECENT_EDIT_BEFORE>\n",
		"old  12",
		"<RECENT_EDIT_AFTER>\n",
		"<RECENT_CHANGE>\n",
		"OLD_TEXT:\nold",
		"NEW_TEXT:\nline",
		"<CURRENT_WINDOW>\n",
		"line 12<CURSOR>",
	} {
		if !strings.Contains(prompt.text, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt.text)
		}
	}
}

func TestEditorTabPromptAvoidsCursorMarkerCollisions(t *testing.T) {
	content := "const marker = \"<CURSOR>\"\nvalue := 1\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: strings.Replace(content, "1", "", 1),
		Line:            2,
		Column:          11,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompt.cursorMarker != "<CURSOR_1>" || !strings.Contains(prompt.text, "Cursor marker: <CURSOR_1>\n") {
		t.Fatalf("cursor marker = %q\nprompt:\n%s", prompt.cursorMarker, prompt.text)
	}
	updated := strings.Replace(prompt.block, "value := 1", "value := 12", 1)
	edit := editorTabPrediction(content, prompt, updated)
	if edit == nil || edit.ExpectedText != "" || edit.InsertText != "2" {
		t.Fatalf("edit = %+v", edit)
	}
}

func TestEditorTabPromptPreservesAnEmptyPreviousDocument(t *testing.T) {
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "new.go",
		Content:         "x",
		PreviousContent: "",
		Line:            1,
		Column:          2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.text, "<RECENT_EDIT_BEFORE>\n\n</RECENT_EDIT_BEFORE>") {
		t.Fatalf("empty original document was not preserved:\n%s", prompt.text)
	}
}

func TestEditorTabPredictionReturnsMultilineInlineEdit(t *testing.T) {
	content := "func f() {\n\tone()\n\ttwo()\n}\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: "func f() {\n\tone\n\ttwo()\n}\n",
		Line:            2,
		Column:          2,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(
		prompt.block,
		"\tone()\n\ttwo()",
		"\tif ready {\n\t\tone()\n\t\ttwo()\n\t}",
		1,
	)
	edit := editorTabPrediction(content, prompt, updated)
	if edit == nil {
		t.Fatal("prediction was discarded")
	}
	if edit.Range.StartLine != 2 || edit.Range.EndLine != 3 {
		t.Fatalf("range = %+v, want a multiline replacement", edit.Range)
	}
	if edit.ExpectedText != "one()\n\ttwo()" || !strings.Contains(edit.InsertText, "if ready") {
		t.Fatalf("edit = %+v", edit)
	}
}

func TestEditorTabPredictionUsesUTF16Columns(t *testing.T) {
	content := "value := \"😀\"\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "emoji.go",
		Content:         content,
		PreviousContent: "value := \"x\"\n",
		Line:            1,
		Column:          13,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(prompt.block, "😀", "😎", 1)
	edit := editorTabPrediction(content, prompt, updated)
	if edit == nil {
		t.Fatal("prediction was discarded")
	}
	if edit.ExpectedText != "😀" || edit.InsertText != "😎" ||
		edit.Range.StartColumn != 11 || edit.Range.EndColumn != 13 {
		t.Fatalf("edit = %+v", edit)
	}
}

func TestEditorTabPredictionNormalizesModelArtifacts(t *testing.T) {
	content := "alpha\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.txt",
		Content:         content,
		PreviousContent: "alph\n",
		Line:            1,
		Column:          6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edit := editorTabPrediction(content, prompt, "alpha"); edit != nil {
		t.Fatalf("missing final newline became an edit: %+v", edit)
	}
	edit := editorTabPrediction(content, prompt, "alpha beta<CURSOR>")
	if edit == nil || edit.InsertText != " beta" || edit.ExpectedText != "" {
		t.Fatalf("normalized edit = %+v", edit)
	}
}

func TestEditorTabPredictionRejectsUnsafeOutput(t *testing.T) {
	content := "alpha\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.txt",
		Content:         content,
		PreviousContent: "alph\n",
		Line:            1,
		Column:          6,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		strings.Repeat("x", editorTabMaxOutput+1),
		"alpha\n1\n2\n3\n4\n5\n6\n7\n8\n9\n",
	} {
		if edit := editorTabPrediction(content, prompt, output); edit != nil {
			t.Fatalf("unsafe output produced edit: %+v", edit)
		}
	}
}

func TestUTF16PositionConversions(t *testing.T) {
	content := "a😀b\nsecond"
	offset, err := utf16PositionOffset(content, 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if content[offset:] != "b\nsecond" {
		t.Fatalf("offset points at %q", content[offset:])
	}
	line, column := offsetUTF16Position(content, offset)
	if line != 1 || column != 4 {
		t.Fatalf("position = %d:%d", line, column)
	}
	if _, err := utf16PositionOffset(content, 1, 3); err == nil {
		t.Fatal("surrogate-splitting column was accepted")
	}
	if _, err := utf16PositionOffset(content, 3, 1); err == nil {
		t.Fatal("line outside the document was accepted")
	}
}

func TestEditorTabModelOverride(t *testing.T) {
	t.Setenv("WINGMAN_MODEL_TAB", "")
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	service := newEditorTabService(cfg)
	if service.modelOverride != "" {
		t.Fatalf("unexpected model override = %q", service.modelOverride)
	}

	t.Setenv("WINGMAN_MODEL_TAB", "gpt-5.6-luna")
	service = newEditorTabService(cfg)
	if service.modelOverride != "gpt-5.6-luna" {
		t.Fatalf("configured model = %q", service.modelOverride)
	}
}

func TestHandleEditorTab(t *testing.T) {
	s := &Server{tab: &editorTabService{
		complete: func(_ context.Context, prompt string) (string, error) {
			if !strings.Contains(prompt, "abc<CURSOR>") {
				t.Fatalf("prompt = %q", prompt)
			}
			return "abcd", nil
		},
	}}
	body := `{"path":"main.go","content":"abc","previous_content":"ab","line":1,"column":4,"version":7}`
	request := httptest.NewRequest(http.MethodPost, "/api/editor/tab", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	s.handleEditorTab(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response editorTabResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Version != 7 || response.Edit == nil || response.Edit.InsertText != "d" || response.Edit.Range.StartColumn != 4 {
		t.Fatalf("response = %+v", response)
	}
}
