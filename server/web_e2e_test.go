//go:build e2e

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/internal/testenv"
)

type webE2EModel struct {
	filePath string

	requests      atomic.Int32
	toolRequests  atomic.Int32
	steerRequests atomic.Int32

	steerStarted chan struct{}
	steerRelease chan struct{}
	steerOnce    sync.Once

	cancelObserved chan struct{}
	cancelOnce     sync.Once
}

func emitE2EEvent(w http.ResponseWriter, event any) {
	data, _ := json.Marshal(event)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func emitE2ETextResponse(w http.ResponseWriter, id, text string) {
	emitE2EEvent(w, map[string]any{
		"type": "response.output_text.delta", "sequence_number": 1,
		"item_id": id, "output_index": 0, "content_index": 0, "delta": text,
	})
	emitE2EEvent(w, map[string]any{
		"type": "response.completed", "sequence_number": 2,
		"response": map[string]any{
			"output": []any{map[string]any{
				"type": "message", "id": id, "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			}},
			"usage": map[string]any{
				"input_tokens": 4, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 2,
			},
		},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func emitE2ETabResponse(w http.ResponseWriter, updatedWindow, editIntent string) {
	w.Header().Set("Content-Type", "application/json")
	structured, _ := json.Marshal(map[string]string{
		"updated_window": updatedWindow,
		"edit_intent":    editIntent,
	})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":     "resp_tab",
		"object": "response",
		"status": "completed",
		"output": []any{map[string]any{
			"id": "msg_tab", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{
				"type": "output_text", "text": string(structured), "annotations": []any{},
			}},
		}},
		"usage": map[string]any{
			"input_tokens": 8, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 2,
		},
	})
}

func emitE2ETabNoop(w http.ResponseWriter) {
	emitE2ETabResponse(w, "", "no_edit")
}

func (m *webE2EModel) handleTool(w http.ResponseWriter) {
	if m.toolRequests.Add(1) == 1 {
		args, _ := json.Marshal(map[string]any{
			"file_path": m.filePath,
			"content":   "created by the browser e2e test\n",
		})
		emitE2EEvent(w, map[string]any{
			"type": "response.output_item.done", "sequence_number": 1, "output_index": 0,
			"item": map[string]any{
				"type": "function_call", "id": "fc_write", "call_id": "call_write",
				"name": "write", "arguments": string(args), "status": "completed",
			},
		})
		emitE2EEvent(w, map[string]any{
			"type": "response.completed", "sequence_number": 2,
			"response": map[string]any{
				"usage": map[string]any{
					"input_tokens": 3, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 1,
				},
			},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	emitE2ETextResponse(w, "msg_tool", "Created the requested file")
}

func (m *webE2EModel) handleSteer(w http.ResponseWriter, r *http.Request) {
	if m.steerRequests.Add(1) == 1 {
		emitE2EEvent(w, map[string]any{
			"type": "response.output_text.delta", "sequence_number": 1,
			"item_id": "msg_steer_1", "output_index": 0, "content_index": 0, "delta": "Working",
		})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		m.steerOnce.Do(func() { close(m.steerStarted) })
		select {
		case <-m.steerRelease:
		case <-r.Context().Done():
			return
		}
		emitE2EEvent(w, map[string]any{
			"type": "response.completed", "sequence_number": 2,
			"response": map[string]any{
				"output": []any{map[string]any{
					"type": "message", "id": "msg_steer_1", "role": "assistant", "status": "completed",
					"content": []any{map[string]any{"type": "output_text", "text": "Working", "annotations": []any{}}},
				}},
				"usage": map[string]any{
					"input_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 1,
				},
			},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	emitE2ETextResponse(w, "msg_steer_2", "Steering applied")
}

func (m *webE2EModel) handleAsk(w http.ResponseWriter, body []byte) {
	var req struct {
		Input []map[string]any `json:"input"`
	}
	_ = json.Unmarshal(body, &req)
	for _, item := range req.Input {
		if item["type"] == "function_call_output" {
			output, _ := item["output"].(string)
			emitE2ETextResponse(w, "msg_ask_done", "You chose: "+strings.TrimSpace(output))
			return
		}
	}

	args, _ := json.Marshal(map[string]any{
		"questions": []any{map[string]any{
			"question": "Which color?",
			"options": []any{
				map[string]any{"label": "Ruby", "description": "warm"},
				map[string]any{"label": "Azure", "description": "cool"},
			},
		}},
	})
	emitE2EEvent(w, map[string]any{
		"type": "response.output_item.done", "sequence_number": 1, "output_index": 0,
		"item": map[string]any{
			"type": "function_call", "id": "fc_ask", "call_id": "call_ask",
			"name": "elicit", "arguments": string(args), "status": "completed",
		},
	})
	emitE2EEvent(w, map[string]any{
		"type": "response.completed", "sequence_number": 2,
		"response": map[string]any{
			"usage": map[string]any{
				"input_tokens": 3, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens": 1,
			},
		},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (m *webE2EModel) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.4","object":"model"}]}`)
	case "/v1/responses":
		m.requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"updated_window"`) && strings.Contains(string(body), `"edit_intent"`) {
			var request struct {
				Input string `json:"input"`
			}
			_ = json.Unmarshal(body, &request)
			if strings.Contains(request.Input, "File: tab-effectiveness.go") &&
				strings.Contains(request.Input, "-    userName := \"Ada\"") &&
				strings.Contains(request.Input, "+    accountName := \"Ada\"") &&
				strings.Contains(request.Input, "println(userName)") {
				emitE2ETabResponse(
					w,
					"    accountName := \"Ada\"\n    prepare()\n    validate()\n    println(accountName)\n}\n",
					"high",
				)
				return
			}
			emitE2ETabNoop(w)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.Contains(string(body), "create e2e-result.txt"):
			m.handleTool(w)
		case strings.Contains(string(body), "cancel this request"):
			emitE2EEvent(w, map[string]any{
				"type": "response.output_text.delta", "sequence_number": 1,
				"item_id": "msg_cancel", "output_index": 0, "content_index": 0, "delta": "Long-running work",
			})
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
			m.cancelOnce.Do(func() { close(m.cancelObserved) })
		case strings.Contains(string(body), "initial request"):
			m.handleSteer(w, r)
		case strings.Contains(string(body), "pick a color"):
			m.handleAsk(w, body)
		case strings.Contains(string(body), "render markdown"):
			emitE2ETextResponse(w, "msg_markdown", "# Migration result\n\n- [x] Streaming\n\n| Language | Status |\n| --- | --- |\n| Go | ready |\n\n```go\npackage main\n\nfunc main() { println(\"ok\") }\n```\n\n```mermaid\ngraph TD; A-->B\n```\n\n```mermaid\nC4Context\n    Person(dev, \"Developer\", \"Uses the app\")\n    System(app, \"Application\", \"Does useful work\")\n    Rel(dev, app, \"Uses\")\n```\n\nMath stays literal: $x^2$.\n\n[Documentation](https://example.com/docs)")
		default:
			http.Error(w, "unknown e2e prompt", http.StatusBadRequest)
		}
	case "/release-steer":
		select {
		case <-m.steerStarted:
		case <-time.After(10 * time.Second):
			http.Error(w, "steering turn did not start", http.StatusGatewayTimeout)
			return
		}
		select {
		case <-m.steerRelease:
		default:
			close(m.steerRelease)
		}
		w.WriteHeader(http.StatusNoContent)
	case "/cancelled":
		select {
		case <-m.cancelObserved:
			w.WriteHeader(http.StatusNoContent)
		case <-time.After(10 * time.Second):
			http.Error(w, "model request was not cancelled", http.StatusGatewayTimeout)
		}
	default:
		http.NotFound(w, r)
	}
}

func TestWebUIE2ECodingAgentWorkflows(t *testing.T) {
	copyTestGopls(t, testenv.WingmanHome(t))
	workDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workDir, "theme-preview.html"),
		[]byte("<!doctype html><title>Theme preview</title><p>Preview</p>"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "readme-preview.md"),
		[]byte("# Markdown preview\n\nRendered **in the browser**.\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "logo-preview.svg"),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30"><rect width="40" height="30" fill="rebeccapurple"/></svg>`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	pixelPNG, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "pixel.png"), pixelPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	previewFiles := map[string]string{
		"data-preview.json": `{ "project": "wingman", "features": ["preview", "edit"] }`,
		"data-preview.yaml": "project: wingman\nfeatures:\n  - preview\n  - edit\n",
		"data-preview.toml": "project = \"wingman\"\nfeatures = [\"preview\", \"edit\"]\n[server]\nport = 8080\n",
		"data-preview.xml":  "<config><project>wingman</project><preview enabled=\"true\"><format>xml</format><format>svg</format></preview></config>",
		"data-preview.csv":  "name,status\nmarkdown,ready\nsvg,ready\n",
		"data-preview.tsv":  "name\tstatus\njson\tready\nyaml\tready\n",
		"flow-preview.mmd":  "flowchart LR\n  Source --> Preview\n  Preview --> Browser\n",
	}
	for name, content := range previewFiles {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "editable.txt"),
		[]byte("original\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "completion.go"),
		[]byte("package main\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "organize-imports.go"),
		[]byte("package main\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "standalone-diagnostics.tsx"),
		[]byte("import { createRoot } from \"react-dom/client\";\nconst view = <main>Hello</main>;\ncreateRoot(document.body).render(view);\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "syntax-highlight.sh"),
		[]byte("#!/usr/bin/env bash\nif [ -n \"$HOME\" ]; then\n  echo \"ready\"\nfi\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	tabFiles := map[string]string{
		"tab-effectiveness.go": "package main\n\nfunc display() {\n    userName := \"Ada\"\n    prepare()\n    validate()\n    println(userName)\n}\n",
		"tab-ghost.go":         "package main\n\nfunc main() {\n\ttotal := price *\n\t_ = total\n}\n",
		"tab-multiline.txt":    "first\nold one\nold two\nlast\n",
		"tab-stale.txt":        "first :=\nsecond :=\n",
	}
	for name, content := range tabFiles {
		if err := os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(workDir, "go.mod"),
		[]byte("module example.com/wingman-e2e\n\ngo 1.24\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	model := &webE2EModel{
		filePath:       filepath.Join(workDir, "e2e-result.txt"),
		steerStarted:   make(chan struct{}),
		steerRelease:   make(chan struct{}),
		cancelObserved: make(chan struct{}),
	}
	modelServer := httptest.NewServer(http.HandlerFunc(model.handler))
	defer modelServer.Close()
	t.Setenv("WINGMAN_URL", modelServer.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, err := New(ctx, workDir, &ServerOptions{NoBrowser: true, disableManagedTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	web := httptest.NewServer(app)
	defer web.Close()

	playwrightArgs := []string{"playwright", "test", "e2e/web.spec.ts", "--config", "playwright.config.ts"}
	grepPattern := os.Getenv("E2E_GREP")
	if grepPattern != "" {
		playwrightArgs = append(playwrightArgs, "--grep", grepPattern)
	}
	cmd := exec.CommandContext(ctx, "npx", playwrightArgs...)
	cmd.Dir = filepath.Join("ui")
	cmd.Env = append(os.Environ(),
		"E2E_BASE_URL="+web.URL,
		"E2E_CONTROL_URL="+modelServer.URL,
		"E2E_WORKSPACE="+workDir,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("playwright after %d model requests: %v\n%s", model.requests.Load(), err, output)
	}
	if grepPattern != "" {
		return
	}

	content, err := os.ReadFile(model.filePath)
	if err != nil {
		t.Fatalf("coding tool did not create file: %v", err)
	}
	if string(content) != "created by the browser e2e test\n" {
		t.Fatalf("created file = %q", content)
	}
}
