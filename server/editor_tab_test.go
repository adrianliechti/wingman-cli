package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/settings"
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
		"Focus: lines 1-26 (<AREA_START> <AREA_END>)\n",
		"Writable: lines 10-17 (<EDIT_START> <EDIT_END>)\n",
		"Cursor marker: <CURSOR>\n",
		"<RECENT_CHANGE>\n",
		"--- pkg/example.go\n+++ pkg/example.go\n@@ line 12 @@",
		"-old  12",
		"+line 12",
		"<CURRENT_FILE>\n",
		"<AREA_START>\nline 01",
		"<EDIT_START>\nline 10",
		"line 12<CURSOR>",
		"<EDIT_END>",
		"<AREA_END>",
	} {
		if !strings.Contains(prompt.text, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt.text)
		}
	}
	for _, unwanted := range []string{"<FILE_CONTEXT>", "<RECENT_EDIT_BEFORE>", "<CURRENT_WINDOW>"} {
		if strings.Contains(prompt.text, unwanted) {
			t.Fatalf("prompt retains redundant block %q:\n%s", unwanted, prompt.text)
		}
	}
	if count := strings.Count(prompt.text, "line 07"); count != 1 {
		t.Fatalf("stable source line appears %d times, want once:\n%s", count, prompt.text)
	}
	if len(prompt.text) > editorTabMaxPrompt {
		t.Fatalf("prompt length = %d, max = %d", len(prompt.text), editorTabMaxPrompt)
	}
}

func TestEditorTabPromptKeepsSmallFileTailVisible(t *testing.T) {
	content := "package main\n\nfunc main() {\n\tcfg := loadConfig()\n\tcfg.Address = \"127.0.0.1\"\n\n\ts, err := server.New(cfg)\n\tif err != nil {\n\t\tpanic(err)\n\t}\n\notel.Setup()\n\tif err := s.ListenAndServe(); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: content,
		Line:            5,
		Column:          7,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"otel.Setup()",
		"s.ListenAndServe()",
	} {
		if strings.Count(prompt.text, want) != 1 {
			t.Fatalf("prompt should contain %q once:\n%s", want, prompt.text)
		}
	}
	if !strings.Contains(editorTabInstructions, "repeat code already in CURRENT_FILE") {
		t.Fatal("Tab instructions must explicitly prohibit duplicating existing code")
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

func TestEditorTabPromptAvoidsMarkersFromDeletedText(t *testing.T) {
	previous := "<EDIT_START><CURSOR><EDIT_END><OMITTED>\nvalue := 1\n"
	content := "value := 1\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: previous,
		Line:            1,
		Column:          11,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Writable: lines 1-2 (<EDIT_START_1> <EDIT_END_1>)",
		"Cursor marker: <CURSOR_1>",
		"Omission marker: <OMITTED_1>",
	} {
		if !strings.Contains(prompt.text, want) {
			t.Fatalf("prompt missing collision-safe marker %q:\n%s", want, prompt.text)
		}
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
	if !strings.Contains(prompt.text, "<RECENT_CHANGE>\n--- new.go\n+++ new.go\n@@ line 1 @@\n-\n+x\n</RECENT_CHANGE>") {
		t.Fatalf("empty original document was not preserved:\n%s", prompt.text)
	}
}

func TestEditorTabPromptKeepsDistantRecentRename(t *testing.T) {
	lines := []string{"package main", "", "func main() {", "\tuserName := \"Ada\""}
	for index := 0; index < 30; index++ {
		lines = append(lines, fmt.Sprintf("\twork(%d)", index))
	}
	useLine := len(lines) + 1
	lines = append(lines, "\tprintln(userName)", "}")
	previous := strings.Join(lines, "\n") + "\n"
	content := strings.Replace(previous, "userName :=", "accountName :=", 1)
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: previous,
		Line:            useLine,
		Column:          len("\tprintln(userName)") + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"-\tuserName := \"Ada\"",
		"+\taccountName := \"Ada\"",
		"println(userName)<CURSOR>",
	} {
		if !strings.Contains(prompt.text, want) {
			t.Fatalf("prompt missing distant rename evidence %q:\n%s", want, prompt.text)
		}
	}
}

func TestEditorTabPromptBoundsLargeFilesWithoutLosingHeaderOrCursor(t *testing.T) {
	lines := []string{"package example", "", `import "fmt"`, ""}
	for index := 0; index < 7_000; index++ {
		lines = append(lines, fmt.Sprintf("var item%04d = fmt.Sprint(%d)", index, index))
	}
	content := strings.Join(lines, "\n") + "\n"
	previous := strings.Replace(content, "item0001", "prior0001", 1)
	cursorIndex := 6_000
	cursorLine := cursorIndex + 5
	cursorText := fmt.Sprintf("var item%04d = fmt.Sprint(%d)", cursorIndex, cursorIndex)
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "large.go",
		Content:         content,
		PreviousContent: previous,
		Line:            cursorLine,
		Column:          len(cursorText) + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prompt.text) > editorTabMaxPrompt {
		t.Fatalf("prompt length = %d, max = %d", len(prompt.text), editorTabMaxPrompt)
	}
	for _, want := range []string{
		"package example",
		`import "fmt"`,
		"-var prior0001 = fmt.Sprint(1)",
		"+var item0001 = fmt.Sprint(1)",
		cursorText + "<CURSOR>",
		"<OMITTED>",
	} {
		if !strings.Contains(prompt.text, want) {
			t.Fatalf("bounded prompt missing %q", want)
		}
	}
	if strings.Contains(prompt.text, "var item3000 =") {
		t.Fatal("bounded prompt retained irrelevant middle-of-file source")
	}
	if count := strings.Count(prompt.text, cursorText); count != 1 {
		t.Fatalf("cursor source appears %d times, want once", count)
	}
}

func TestEditorTabPromptRejectsUnboundedPath(t *testing.T) {
	_, err := buildEditorTabPrompt(editorTabRequest{
		Path:            strings.Repeat("x", editorTabMaxPath+1),
		Content:         "x",
		PreviousContent: "",
		Line:            1,
		Column:          2,
	})
	if err == nil {
		t.Fatal("unbounded path was accepted")
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

func TestEditorTabPredictionDropsReemittedContextAroundCursorEdit(t *testing.T) {
	content := "if\n\nh := make(textproto.MIMEHeader)\nh.Set(\"A\", a)\nh.Set(\"B\", b)\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "client.go",
		Content:         content,
		PreviousContent: strings.TrimPrefix(content, "if"),
		Line:            1,
		Column:          3,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := "if file.Content == nil || len(file.Content) == 0 {\n" +
		"\treturn nil, errors.New(\"file content is empty\")\n}\n\n" +
		"h := make(textproto.MIMEHeader)\nh.Set(\"A\", a)\nh.Set(\"B\", changed)\n"
	edit := editorTabPrediction(content, prompt, updated)
	if edit == nil {
		t.Fatal("cursor edit was discarded")
	}
	if edit.Range.StartLine != 1 || edit.Range.StartColumn != 3 || edit.ExpectedText != "" {
		t.Fatalf("edit range is not minimal: %+v", edit)
	}
	if !strings.Contains(edit.InsertText, "file.Content") || strings.Contains(edit.InsertText, "h :=") {
		t.Fatalf("edit includes re-emitted context: %+v", edit)
	}
}

func TestEditorTabChangeAroundCursorRecognizesReemittedLastLine(t *testing.T) {
	original := "\tout.\n" +
		"\tout.WriteString(content[areaStart:blockStart])\n" +
		"\tout.WriteString(markers.start)\n" +
		"\tout.WriteString(content[blockStart:cursor])\n" +
		"\tout.WriteString(markers.cursor)\n" +
		"\tout.WriteString(content[cursor:blockEnd])\n"
	updated := "\tout.\n\tout.WriteString(content[cursor:blockEnd])\n"
	change, ok := editorTabChangeAroundCursor(original, updated, len("\tout."))
	if !ok {
		t.Fatal("change was not found")
	}
	if insert := updated[change.AfterStart:change.AfterEnd]; insert != "" {
		t.Fatalf("re-emitted last line became replacement text %q", insert)
	}
	if expected := original[change.BeforeStart:change.BeforeEnd]; !strings.Contains(expected, "markers.start") || strings.Contains(expected, "cursor:blockEnd") {
		t.Fatalf("wrong deleted lines %q", expected)
	}
}

func TestEditorTabChangeAroundCursorSeparatesFormattingFromNextEdit(t *testing.T) {
	original := "\tcontextBudget := sourceBudget - headerBudget\n\textra := localBudget - areaBytes\n"
	updated := "contextBudget := sourceBudget - headerBudget\n\textra := contextBudget - areaBytes\n"
	cursor := len("\tcontextBudget := sourceBudget - headerBudget")
	change, ok := editorTabChangeAroundCursor(original, updated, cursor)
	if !ok {
		t.Fatal("change was not found")
	}
	if expected := original[change.BeforeStart:change.BeforeEnd]; expected != "local" {
		t.Fatalf("expected text = %q, want the semantic follow-up only", expected)
	}
	if insert := updated[change.AfterStart:change.AfterEnd]; insert != "context" {
		t.Fatalf("insert text = %q, want the semantic follow-up only", insert)
	}
}

func TestEditorTabPredictionDoesNotUndoRecentInsertion(t *testing.T) {
	content := "if\nnext()\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: "next()\n",
		Line:            1,
		Column:          3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edit := editorTabPrediction(content, prompt, "next()\n"); edit != nil {
		t.Fatalf("recent user insertion was deleted: %+v", edit)
	}
}

func TestEditorTabPredictionAllowsUnrelatedDeletion(t *testing.T) {
	previous := "new\nkeep\nobsolete\n"
	content := "new value\nkeep\nobsolete\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: previous,
		Line:            3,
		Column:          len("obsolete") + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	edit := editorTabPrediction(content, prompt, "new value\nkeep\n\n")
	if edit == nil || edit.ExpectedText != "obsolete" || edit.InsertText != "" {
		t.Fatalf("unrelated deletion was discarded: %+v", edit)
	}
}

func TestEditorTabPromptKeepsDistantDeclarationsReadOnly(t *testing.T) {
	content := "func (c *Client) Extract() {}\n\none\ntwo\nthree\nfour\nfive\nsix\nif\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "client.go",
		Content:         content,
		PreviousContent: strings.TrimSuffix(content, "if\n") + "\n",
		Line:            9,
		Column:          3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.block, "func (c *Client) Extract") {
		t.Fatalf("distant declaration became writable:\n%s", prompt.block)
	}
	if count := strings.Count(prompt.text, "func (c *Client) Extract"); count != 1 {
		t.Fatalf("declaration appears %d times in prompt, want once:\n%s", count, prompt.text)
	}
}

func TestEditorTabPredictionAllowsUsefulLongInsertion(t *testing.T) {
	content := "value := 1\n"
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: content,
		Line:            1,
		Column:          len("value := 1") + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := prompt.block + "\nif ready {\n\tone()\n\ttwo()\n\tthree()\n\tfour()\n\tfive()\n\tsix()\n}"
	if edit := editorTabPrediction(content, prompt, updated); edit == nil {
		t.Fatal("useful multiline insertion was discarded")
	}
}

func TestEditorTabPredictionKeepsFollowUpEditMinimal(t *testing.T) {
	previous := "package main\n\nfunc display() {\n    userName := \"Ada\"\n    println(userName)\n}\n"
	content := strings.Replace(previous, "userName :=", "accountName :=", 1)
	cursorLine := 4
	cursorColumn := len(`    accountName := "Ada"`) + 1
	prompt, err := buildEditorTabPrompt(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: previous,
		Line:            cursorLine,
		Column:          cursorColumn,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(prompt.block, "println(userName)", "println(accountName)", 1)
	edit := editorTabPrediction(content, prompt, updated)
	if edit == nil {
		t.Fatal("follow-up prediction was discarded")
	}
	if edit.Range.StartLine != 5 || edit.Range.EndLine != 5 {
		t.Fatalf("range = %+v, want a minimal next-line edit", edit.Range)
	}
	if edit.ExpectedText != "user" || edit.InsertText != "account" {
		t.Fatalf("minimal edit = %+v", edit)
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
	edit := editorTabPrediction(
		content,
		prompt,
		prompt.editStartMarker+"alpha beta"+prompt.cursorMarker+prompt.editEndMarker,
	)
	if edit == nil || edit.InsertText != " beta" || edit.ExpectedText != "" {
		t.Fatalf("normalized edit = %+v", edit)
	}
	if edit := editorTabPrediction(content, prompt, prompt.omitMarker+"alpha beta"); edit != nil {
		t.Fatalf("omitted source marker produced edit: %+v", edit)
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
		prompt.areaStartMarker + "alpha",
		prompt.areaEndMarker + "alpha",
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

func TestEditorTabGenerationTargetUsesCompatibleLowestEffort(t *testing.T) {
	cfg := &agent.Config{
		Model: func() string { return "main-model" },
		RoleModel: func(role string) (agent.ModelOption, bool) {
			if role != "utility" {
				return agent.ModelOption{}, false
			}
			return agent.ModelOption{
				ID:      "kimi-k3",
				Efforts: []string{"low", "high", "max"},
			}, true
		},
	}
	modelID, effort := resolveGenerationTarget(cfg, "utility", "")
	if modelID != "kimi-k3" || effort != "low" {
		t.Fatalf("utility target = %q/%q", modelID, effort)
	}
	modelID, effort = resolveGenerationTarget(cfg, "utility", "gpt-5.6-luna")
	if modelID != "gpt-5.6-luna" || effort != "none" {
		t.Fatalf("override target = %q/%q", modelID, effort)
	}
	modelID, effort = resolveGenerationTarget(&agent.Config{
		Model: func() string { return "custom-model" },
	}, "utility", "")
	if modelID != "custom-model" || effort != "" {
		t.Fatalf("unknown target = %q/%q", modelID, effort)
	}
}

func TestHandleEditorTab(t *testing.T) {
	s := &Server{tab: &editorTabService{
		complete: func(_ context.Context, prompt string) (editorTabCompletion, error) {
			if !strings.Contains(prompt, "abc<CURSOR>") {
				t.Fatalf("prompt = %q", prompt)
			}
			return editorTabCompletion{UpdatedWindow: "abcd", EditIntent: "high"}, nil
		},
	}}
	s.tabEnabled.Store(true)
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

func TestEditorTabFiltersLowIntent(t *testing.T) {
	s := &editorTabService{
		complete: func(context.Context, string) (editorTabCompletion, error) {
			return editorTabCompletion{UpdatedWindow: "value.String()", EditIntent: "low"}, nil
		},
	}
	response, err := s.predict(context.Background(), editorTabRequest{
		Path:            "main.go",
		Content:         "value.",
		PreviousContent: "value",
		Line:            1,
		Column:          7,
		Version:         4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Version != 4 || response.Edit != nil {
		t.Fatalf("low-confidence response was shown: %+v", response)
	}
}

func TestEditorTabShowsMediumIntentByDefault(t *testing.T) {
	s := &editorTabService{
		complete: func(context.Context, string) (editorTabCompletion, error) {
			return editorTabCompletion{UpdatedWindow: "value.String()", EditIntent: "medium"}, nil
		},
	}
	response, err := s.predict(context.Background(), editorTabRequest{
		Path:            "main.go",
		Content:         "value.",
		PreviousContent: "value",
		Line:            1,
		Column:          7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Edit == nil || response.Edit.InsertText != "String()" {
		t.Fatalf("medium-confidence response was hidden: %+v", response)
	}
}

func TestHandleEditorTabPropagatesDistantRename(t *testing.T) {
	lines := []string{"package main", "", "func main() {", "\tuserName := \"Ada\""}
	for index := 0; index < 30; index++ {
		lines = append(lines, fmt.Sprintf("\twork(%d)", index))
	}
	useLine := len(lines) + 1
	lines = append(lines, "\tprintln(userName)", "}")
	previous := strings.Join(lines, "\n") + "\n"
	content := strings.Replace(previous, "userName :=", "accountName :=", 1)
	_, _, _, _, block := editorLineWindow(content, useLine, editorTabEditLinesAbove, editorTabEditLinesBelow)

	s := &Server{tab: &editorTabService{
		complete: func(_ context.Context, prompt string) (editorTabCompletion, error) {
			for _, want := range []string{"-\tuserName := \"Ada\"", "+\taccountName := \"Ada\"", "println(userName)<CURSOR>"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("model prompt missing %q:\n%s", want, prompt)
				}
			}
			return editorTabCompletion{
				UpdatedWindow: strings.Replace(block, "println(userName)", "println(accountName)", 1),
				EditIntent:    "high",
			}, nil
		},
	}}
	s.tabEnabled.Store(true)
	body, err := json.Marshal(editorTabRequest{
		Path:            "main.go",
		Content:         content,
		PreviousContent: previous,
		Line:            useLine,
		Column:          len("\tprintln(userName)") + 1,
		Version:         11,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/editor/tab", strings.NewReader(string(body)))
	recorder := httptest.NewRecorder()
	s.handleEditorTab(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response editorTabResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Edit == nil {
		t.Fatal("rename follow-up did not produce an edit")
	}
	start, err := utf16PositionOffset(content, response.Edit.Range.StartLine, response.Edit.Range.StartColumn)
	if err != nil {
		t.Fatal(err)
	}
	end, err := utf16PositionOffset(content, response.Edit.Range.EndLine, response.Edit.Range.EndColumn)
	if err != nil {
		t.Fatal(err)
	}
	updated := content[:start] + response.Edit.InsertText + content[end:]
	if strings.Count(updated, "accountName") != 2 || strings.Contains(updated, "userName") {
		t.Fatalf("applied completion did not propagate rename:\n%s", updated)
	}
}

func TestHandleEditorTabHonorsDisabledSetting(t *testing.T) {
	called := false
	s := &Server{tab: &editorTabService{
		complete: func(context.Context, string) (editorTabCompletion, error) {
			called = true
			return editorTabCompletion{}, nil
		},
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/editor/tab", strings.NewReader(`{}`))
	recorder := httptest.NewRecorder()
	s.handleEditorTab(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("disabled Tab called the model")
	}
}

func TestEditorTabRequestsCancelOlderRequest(t *testing.T) {
	s := &Server{}
	first, finishFirst := s.beginEditorTabRequest(context.Background())
	second, finishSecond := s.beginEditorTabRequest(context.Background())
	select {
	case <-first.Done():
	default:
		t.Fatal("older request was not cancelled")
	}
	if err := second.Err(); err != nil {
		t.Fatalf("newest request was cancelled: %v", err)
	}
	finishFirst()
	if err := second.Err(); err != nil {
		t.Fatalf("finishing the older request cancelled the newest: %v", err)
	}
	finishSecond()
}

func TestHandleEditorTabSettingsPersistsPreference(t *testing.T) {
	testenv.WingmanHome(t)
	s := &Server{tab: &editorTabService{}}
	s.tabEnabled.Store(true)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/settings/editor.tab.completion",
		strings.NewReader(`{"editor.tab.completion":false}`),
	)
	recorder := httptest.NewRecorder()
	s.handleEditorTabSettings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if s.tabEnabled.Load() {
		t.Fatal("server preference was not disabled")
	}
	value, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if value.EditorTabCompletion {
		t.Fatal("disabled editor.tab.completion was not persisted")
	}
}
