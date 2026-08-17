package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

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
	cases := []struct {
		name     string
		request  editorTabRequest
		wantText string
	}{
		{
			name: "continue-expression",
			request: editorTabRequest{
				Path:            "greeting.go",
				PreviousContent: "package greeting\n\nfunc Hello(name string) string {\n\treturn \"\"\n}\n",
				Content:         "package greeting\n\nfunc Hello(name string) string {\n\treturn \"Hello, \" +\n}\n",
				Line:            4,
				Column:          20,
			},
			wantText: "name",
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
			wantText: "account",
		},
	}

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
				updated, err := service.complete(ctx, prompt.text)
				elapsed := time.Since(started)
				if err != nil {
					t.Fatalf("after %s: %v", elapsed.Round(time.Millisecond), err)
				}
				t.Logf("updated_window=%q", clipLiveLog(updated))
				edit := editorTabPrediction(test.request.Content, prompt, updated)
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
				if !strings.Contains(edit.InsertText, test.wantText) {
					t.Errorf("prediction does not contain %q", test.wantText)
				}
			})
		}
	}
}

func clipLiveLog(value string) string {
	const limit = 300
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
