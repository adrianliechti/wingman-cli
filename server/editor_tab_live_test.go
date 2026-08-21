package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

type liveEditorTabCase struct {
	name         string
	request      editorTabRequest
	wantExpected string
	wantInsert   string
	wantNoEdit   bool
}

// TestLiveEditorTabModels is an opt-in latency and quality smoke test against
// the configured Responses API. It never runs in the normal test suite.
func TestLiveEditorTabModels(t *testing.T) {
	modelList := strings.TrimSpace(os.Getenv("WINGMAN_TAB_LIVE_MODELS"))
	if modelList == "" {
		t.Skip("set WINGMAN_TAB_LIVE_MODELS to run the live Tab smoke test")
	}
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cases := []liveEditorTabCase{
		{
			name: "continue-expression",
			request: editorTabRequest{
				Path:            "greeting.go",
				PreviousContent: "package greeting\n\nfunc Hello(name string) string {\n\treturn \"\"\n}\n",
				Content:         "package greeting\n\nfunc Hello(name string) string {\n\treturn \"Hello, \" +\n}\n",
				Line:            4,
				Column:          20,
			},
			wantInsert: " name",
		},
		{
			name: "finish-rename",
			request: editorTabRequest{
				Path:            "user.go",
				PreviousContent: "package user\n\ntype User struct { Name string }\n\nfunc DisplayName(user User) string {\n\treturn user.Name\n}\n",
				Content:         "package user\n\ntype User struct { Name string }\n\nfunc DisplayName(account User) string {\n\treturn user.Name\n}\n",
				Line:            5,
				Column:          25,
			},
			wantExpected: "user",
			wantInsert:   "account",
		},
	}
	cases = append(cases, liveEditorTabRepositoryCases(t)...)

	for _, model := range strings.Split(modelList, ",") {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		service := newEditorTabServiceForModel(cfg, model)
		for _, test := range cases {
			t.Run(model+"/"+test.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				started := time.Now()
				prompt, err := buildEditorTabPrompt(test.request)
				if err != nil {
					t.Fatal(err)
				}
				completion, err := service.complete(ctx, prompt.text)
				elapsed := time.Since(started)
				if err != nil {
					t.Fatalf("after %s: %v", elapsed.Round(time.Millisecond), err)
				}
				t.Logf("edit_intent=%s updated_window=%q", completion.EditIntent, clipLiveLog(completion.UpdatedWindow))
				var edit *editorTabEdit
				if editorTabShowsIntent(completion.EditIntent) {
					edit = editorTabPrediction(test.request.Content, prompt, completion.UpdatedWindow)
				}
				if test.wantNoEdit {
					if edit != nil {
						t.Fatalf("after %s: unexpected edit: %+v", elapsed.Round(time.Millisecond), edit)
					}
					return
				}
				if edit == nil {
					t.Fatalf("after %s: no edit", elapsed.Round(time.Millisecond))
				}
				t.Logf(
					"latency=%s range=%+v expected=%q insert=%q",
					elapsed.Round(time.Millisecond),
					edit.Range,
					clipLiveLog(edit.ExpectedText),
					clipLiveLog(edit.InsertText),
				)
				if edit.Range.StartLine < test.request.Line || edit.Range.EndLine > test.request.Line+editorTabEditLinesBelow {
					t.Errorf("edit escaped cursor-led writable window: %+v", edit.Range)
				}
				if edit.ExpectedText != test.wantExpected {
					t.Errorf("expected text = %q, want %q", edit.ExpectedText, test.wantExpected)
				}
				if edit.InsertText != test.wantInsert {
					t.Errorf("insert text = %q, want %q", edit.InsertText, test.wantInsert)
				}
			})
		}
	}
}

func liveEditorTabRepositoryCases(t *testing.T) []liveEditorTabCase {
	t.Helper()
	goSource, err := os.ReadFile("editor_tab.go")
	if err != nil {
		t.Fatal(err)
	}
	tsSource, err := os.ReadFile("ui/src/monacoTab.ts")
	if err != nil {
		t.Fatal(err)
	}
	testenvSource, err := os.ReadFile("../internal/testenv/testenv.go")
	if err != nil {
		t.Fatal(err)
	}
	return []liveEditorTabCase{
		{
			name: "real-go-member-completion",
			request: liveEditorTabInsertion(
				t,
				"server/editor_tab.go",
				string(goSource),
				"\tout.WriteString(content[start:areaStart])",
				"\tout.",
			),
			wantInsert: "WriteString(content[start:areaStart])",
		},
		{
			name: "real-go-follow-rename",
			request: liveEditorTabReplacement(
				t,
				"server/editor_tab.go",
				string(goSource),
				"\tlocalBudget := sourceBudget - headerBudget",
				"\tcontextBudget := sourceBudget - headerBudget",
			),
			wantExpected: "local",
			wantInsert:   "context",
		},
		{
			name: "real-typescript-member-completion",
			request: liveEditorTabInsertion(
				t,
				"server/ui/src/monacoTab.ts",
				string(tsSource),
				"\t\t\t\tconst content = candidateModel.getValue();",
				"\t\t\t\tconst content = candidateModel.",
			),
			wantInsert: "getValue();",
		},
		{
			name: "real-go-blank-line-does-not-repeat-neighbor",
			request: liveEditorTabInsertAfter(
				t,
				"internal/testenv/testenv.go",
				string(testenvSource),
				"\tdefer os.RemoveAll(wingmanHome)\n",
				"\n",
			),
			wantNoEdit: true,
		},
		{
			name: "real-go-ambiguous-if-does-not-repeat-function",
			request: liveEditorTabInsertBefore(
				t,
				"server/editor_tab.go",
				string(goSource),
				"\tmarkerBytes := len(markers.areaStart)",
				"\tif",
			),
			wantNoEdit: true,
		},
	}
}

func liveEditorTabInsertAfter(t *testing.T, path, source, after, inserted string) editorTabRequest {
	t.Helper()
	index := strings.Index(source, after)
	if index < 0 {
		t.Fatalf("%s does not contain %q", path, after)
	}
	index += len(after)
	current := source[:index] + inserted + source[index:]
	line, column := offsetUTF16Position(current, index+len(inserted))
	return editorTabRequest{Path: path, PreviousContent: source, Content: current, Line: line, Column: column}
}

func liveEditorTabInsertBefore(t *testing.T, path, source, before, typed string) editorTabRequest {
	t.Helper()
	index := strings.Index(source, before)
	if index < 0 {
		t.Fatalf("%s does not contain %q", path, before)
	}
	current := source[:index] + typed + "\n" + source[index:]
	line, column := offsetUTF16Position(current, index+len(typed))
	return editorTabRequest{Path: path, PreviousContent: source, Content: current, Line: line, Column: column}
}

func liveEditorTabInsertion(t *testing.T, path, source, replaced, typed string) editorTabRequest {
	t.Helper()
	index := strings.Index(source, replaced)
	if index < 0 {
		t.Fatalf("%s does not contain %q", path, replaced)
	}
	previous := source[:index] + source[index+len(replaced):]
	current := source[:index] + typed + source[index+len(replaced):]
	line, column := offsetUTF16Position(current, index+len(typed))
	return editorTabRequest{Path: path, PreviousContent: previous, Content: current, Line: line, Column: column}
}

func liveEditorTabReplacement(t *testing.T, path, source, oldText, newText string) editorTabRequest {
	t.Helper()
	index := strings.Index(source, oldText)
	if index < 0 {
		t.Fatalf("%s does not contain %q", path, oldText)
	}
	current := source[:index] + newText + source[index+len(oldText):]
	line, column := offsetUTF16Position(current, index+len(newText))
	return editorTabRequest{Path: path, PreviousContent: source, Content: current, Line: line, Column: column}
}

func clipLiveLog(value string) string {
	const limit = 300
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
