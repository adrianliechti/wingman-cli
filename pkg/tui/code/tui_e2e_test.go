//go:build e2e

package code

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	codeagent "github.com/adrianliechti/wingman-agent/pkg/code/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/clipboard"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func tuiModelServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.4","object":"model"}]}`)
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"E2E reply\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\ndata: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
}

func waitForTUI(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for TUI state")
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ansi.Strip(b.buf.String())
}

type tuiE2EHarness struct {
	agent     *codeagent.Agent
	app       *App
	sessionID string
	input     io.Writer
	output    *syncBuffer
}

func (h *tuiE2EHarness) postText(t *testing.T, value string) {
	t.Helper()
	if _, err := io.WriteString(h.input, value+"\r"); err != nil {
		t.Fatal(err)
	}
}

func newTUIE2EHarness(t *testing.T) *tuiE2EHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	workspace := newTestWorkspace(t, t.TempDir())
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	codeAgent := codeagent.New(workspace, cfg, nil)
	sessionID, err := codeAgent.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	app := New(ctx, codeAgent, sessionID)
	codeAgent.SetUI(app)

	inR, inW := io.Pipe()
	out := &syncBuffer{}

	term := inline.NewTerminal(inline.WithIO(inR, out, func() (int, int) { return 100, 35 }))
	app.WithTerminal(term)

	runDone := make(chan error, 1)
	go func() { runDone <- app.Run() }()

	t.Cleanup(func() {
		app.stop()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("TUI run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("TUI did not stop")
		}
		inW.Close()
		workspace.Close()
		cancel()
	})

	waitForTUI(t, func() bool { return app.getPhase() == PhaseIdle })
	return &tuiE2EHarness{agent: codeAgent, app: app, sessionID: sessionID, input: inW, output: out}
}

func TestTUIE2ESendsAndRendersTurn(t *testing.T) {
	model := tuiModelServer(t)
	defer model.Close()
	t.Setenv("WINGMAN_URL", model.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)
	h.postText(t, "hello e2e")

	waitForTUI(t, func() bool {
		messages := h.agent.Messages(h.sessionID)
		return len(messages) >= 2 && messages[len(messages)-1].Role == agent.RoleAssistant
	})
	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "E2E reply") })

	messages := h.agent.Messages(h.sessionID)
	if messages[0].Role != agent.RoleUser || messages[0].Content[0].Text != "hello e2e" {
		t.Fatalf("user message = %+v", messages[0])
	}
}

func TestTUIE2EPasteWaitsForExplicitSubmit(t *testing.T) {
	model := tuiModelServer(t)
	defer model.Close()
	t.Setenv("WINGMAN_URL", model.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")
	const pasted = "pasted first line\r\n第二行 🦋"
	for name, sequence := range map[string]string{
		"Ctrl+V":       "\x16",
		"Ctrl+Alt+V":   "\x1b\x16",
		"CSI-u":        "\x1b[118;5:1u\x1b[118;5:3u",
		"xterm":        "\x1b[27;5;118~",
		"native paste": "\x1b[200~" + pasted + "\x1b[201~",
	} {
		t.Run(name, func(t *testing.T) {
			h := newTUIE2EHarness(t)
			var reads atomic.Int32
			ready := make(chan struct{})
			h.app.post(func() {
				h.app.clipboardRead = func() ([]clipboard.Content, error) {
					reads.Add(1)
					return []clipboard.Content{{Text: pasted}}, nil
				}
				close(ready)
			})
			<-ready
			if _, err := io.WriteString(h.input, sequence); err != nil {
				t.Fatal(err)
			}
			waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "第二行") })
			if messages := h.agent.Messages(h.sessionID); len(messages) != 0 {
				t.Fatalf("paste submitted without Enter: %+v", messages)
			}
			wantReads := int32(1)
			if name == "native paste" {
				wantReads = 0
			}
			if reads.Load() != wantReads {
				t.Fatalf("clipboard reads = %d, want %d", reads.Load(), wantReads)
			}
			h.postText(t, "")
			waitForTUI(t, func() bool { return len(h.agent.Messages(h.sessionID)) >= 2 })
			messages := h.agent.Messages(h.sessionID)
			if text := messages[0].Content[0].Text; text != "pasted first line\n第二行 🦋" {
				t.Fatalf("submitted paste = %q", text)
			}
		})
	}
}

type tuiToolRoundModel struct {
	requests atomic.Int32
}

func (m *tuiToolRoundModel) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.4","object":"model"}]}`)
	case "/v1/responses":
		w.Header().Set("Content-Type", "text/event-stream")
		if m.requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_before\",\"output_index\":0,\"content_index\":0,\"delta\":\"Before tool.\"}\n\n")
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_before\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"Before tool.\",\"annotations\":[]}]},{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"glob\",\"arguments\":\"{\\\"pattern\\\":\\\"*\\\"}\",\"status\":\"completed\"}],\"usage\":{\"input_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_after\",\"output_index\":0,\"content_index\":0,\"delta\":\"After tool.\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_after\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"After tool.\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\n")
	default:
		http.NotFound(w, r)
	}
}

func TestTUIE2ENativeToolRoundKeepsOrder(t *testing.T) {
	model := &tuiToolRoundModel{}
	modelServer := httptest.NewServer(http.HandlerFunc(model.handler))
	defer modelServer.Close()
	t.Setenv("WINGMAN_URL", modelServer.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)
	h.postText(t, "run a tool")
	waitForTUI(t, func() bool {
		messages := h.agent.Messages(h.sessionID)
		return h.app.getPhase() == PhaseIdle && len(messages) >= 4 && strings.Contains(finalMessageText(messages), "After tool.")
	})

	transcript := ansi.Strip(strings.Join(h.app.chatViewLines(100), "\n"))
	beforeAt := strings.Index(transcript, "Before tool.")
	toolAt := strings.Index(transcript, "glob *")
	afterAt := strings.Index(transcript, "After tool.")
	if beforeAt < 0 || toolAt <= beforeAt || afterAt <= toolAt {
		t.Fatalf("native tool round is out of order: %q", transcript)
	}
	if strings.Count(transcript, "Before tool.") != 1 || strings.Count(transcript, "After tool.") != 1 {
		t.Fatalf("native tool round contains duplicated assistant text: %q", transcript)
	}
}

func finalMessageText(messages []agent.Message) string {
	if len(messages) == 0 {
		return ""
	}
	var text strings.Builder
	for _, content := range messages[len(messages)-1].Content {
		text.WriteString(content.Text)
	}
	return text.String()
}

func TestTUIE2ECommandCenterAndTranscriptSearch(t *testing.T) {
	model := tuiModelServer(t)
	defer model.Close()
	t.Setenv("WINGMAN_URL", model.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)
	if _, err := io.WriteString(h.input, "\x10"); err != nil { // ctrl+p
		t.Fatal(err)
	}
	waitForTUI(t, func() bool { return h.app.popup != nil && h.app.popup.kind == popupPalette })
	if !strings.Contains(h.output.Text(), "commands") {
		t.Fatal("command center was not rendered")
	}
	if _, err := io.WriteString(h.input, "\x10"); err != nil {
		t.Fatal(err)
	}
	waitForTUI(t, func() bool { return h.app.popup == nil })

	h.postText(t, "hello transcript")
	waitForTUI(t, func() bool {
		messages := h.agent.Messages(h.sessionID)
		return len(messages) >= 2 && messages[len(messages)-1].Role == agent.RoleAssistant
	})

	if _, err := io.WriteString(h.input, "\x0f"); err != nil { // ctrl+o
		t.Fatal(err)
	}
	waitForTUI(t, func() bool {
		_, ok := h.app.overlay.(*transcriptOverlay)
		return ok
	})
	if _, err := io.WriteString(h.input, "/reply\r"); err != nil {
		t.Fatal(err)
	}
	waitForTUI(t, func() bool {
		o, ok := h.app.overlay.(*transcriptOverlay)
		return ok && o.query == "reply" && len(o.matches) == 1
	})
}

func TestTUIE2EConfirmAndElicitPopups(t *testing.T) {
	model := tuiModelServer(t)
	defer model.Close()
	t.Setenv("WINGMAN_URL", model.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)

	confirmed := make(chan bool, 1)
	go func() {
		ok, _ := h.app.Confirm(context.Background(), "run ls?")
		confirmed <- ok
	}()

	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "Confirm command") })
	if _, err := io.WriteString(h.input, "y"); err != nil {
		t.Fatal(err)
	}

	select {
	case ok := <-confirmed:
		if !ok {
			t.Fatal("confirm hotkey y returned false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("confirm did not resolve")
	}

	elicited := make(chan tool.ElicitResult, 1)
	go func() {
		res, _ := h.app.Elicit(context.Background(), tool.ElicitRequest{
			Message: "pick a color",
			Fields:  []tool.ElicitField{{Name: "color", Enum: []string{"red", "green", "blue"}, Strict: true}},
		})
		elicited <- res
	}()

	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "pick a color") })
	if _, err := io.WriteString(h.input, "\x1b[B\r"); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-elicited:
		if res.Action != tool.ElicitAccept || res.Content["color"] != "green" {
			t.Fatalf("elicit result = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("elicit did not resolve")
	}
}

func TestTUIE2ECustomAnswerCompanion(t *testing.T) {
	model := tuiModelServer(t)
	defer model.Close()
	t.Setenv("WINGMAN_URL", model.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)
	elicited := make(chan tool.ElicitResult, 1)
	go func() {
		res, _ := h.app.Elicit(context.Background(), tool.ElicitRequest{
			Message: "pick a color",
			Fields: []tool.ElicitField{
				{Name: "question_0", Enum: []string{"red", "green"}, Strict: true},
				{Name: "question_0_custom", Title: "Other", Description: "Type your own answer.", CustomAnswerFor: "question_0"},
			},
		})
		elicited <- res
	}()

	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "pick a color") })
	if _, err := io.WriteString(h.input, "\x1b[B\x1b[B\r"); err != nil {
		t.Fatal(err)
	}
	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "Type your own answer.") })
	if _, err := io.WriteString(h.input, "purple\r"); err != nil {
		t.Fatal(err)
	}

	select {
	case res := <-elicited:
		if res.Action != tool.ElicitAccept || res.Content["question_0_custom"] != "purple" {
			t.Fatalf("custom elicit result = %+v", res)
		}
		if _, exists := res.Content["question_0"]; exists {
			t.Fatalf("custom answer should replace the enum answer: %+v", res.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("custom elicitation did not resolve")
	}
}

type tuiSteeringModel struct {
	requests atomic.Int32
	release  chan struct{}
}

func (m *tuiSteeringModel) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/models":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5.4","object":"model"}]}`)
	case "/v1/responses":
		w.Header().Set("Content-Type", "text/event-stream")
		if m.requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"Working\"}\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case <-m.release:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"Working\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_2\",\"output_index\":0,\"content_index\":0,\"delta\":\"Steering applied\"}\n\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_2\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"Steering applied\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":4,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":2}}}\n\ndata: [DONE]\n\n")
	default:
		http.NotFound(w, r)
	}
}

func TestTUIE2ESteersActiveTurn(t *testing.T) {
	model := &tuiSteeringModel{release: make(chan struct{})}
	modelServer := httptest.NewServer(http.HandlerFunc(model.handler))
	defer modelServer.Close()
	t.Setenv("WINGMAN_URL", modelServer.URL)
	t.Setenv("WINGMAN_MODEL", "gpt-5.4")
	t.Setenv("WINGMAN_CALLER", "e2e")

	h := newTUIE2EHarness(t)
	h.postText(t, "initial request")
	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "Working") })

	h.postText(t, "steer this turn")
	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "steer this turn") })
	close(model.release)

	waitForTUI(t, func() bool {
		messages := h.agent.Messages(h.sessionID)
		return len(messages) >= 4 && messages[len(messages)-1].Role == agent.RoleAssistant
	})
	waitForTUI(t, func() bool { return strings.Contains(h.output.Text(), "Steering applied") })

	messages := h.agent.Messages(h.sessionID)
	want := []struct {
		role agent.MessageRole
		text string
	}{
		{agent.RoleUser, "initial request"},
		{agent.RoleAssistant, "Working"},
		{agent.RoleUser, "steer this turn"},
		{agent.RoleAssistant, "Steering applied"},
	}
	if len(messages) != len(want) {
		t.Fatalf("messages = %+v", messages)
	}
	for i, expected := range want {
		if messages[i].Role != expected.role || len(messages[i].Content) == 0 || messages[i].Content[0].Text != expected.text {
			t.Fatalf("message %d = %+v, want %s %q", i, messages[i], expected.role, expected.text)
		}
	}
}
