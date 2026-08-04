package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/adrianliechti/wingman-agent/pkg/acp/internal/acptest"
	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func TestACPContract(t *testing.T) {
	acptest.Run(t, newContractServer)
}

type contractServer struct {
	*Server
	model *httptest.Server
}

func newContractServer(t *testing.T) acptest.Agent {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "WINGMAN_URL")
	unsetEnv(t, "WINGMAN_TOKEN")
	unsetEnv(t, "WINGMAN_MODEL")
	t.Setenv("OPENAI_API_KEY", "contract-key")
	t.Setenv("OPENAI_DEFAULT_MODEL", "contract-model")

	backend := &contractModelBackend{}
	model := httptest.NewServer(backend)
	t.Setenv("OPENAI_BASE_URL", model.URL)

	cfg, err := agent.DefaultConfig()
	if err != nil {
		model.Close()
		t.Fatal(err)
	}
	return &contractServer{
		Server: &Server{
			config:      cfg,
			sessions:    map[acpsdk.SessionId]*sessionEntry{},
			sessionDirs: map[acpsdk.SessionId]string{},
			workspaces:  map[string]*workspaceEntry{},
		},
		model: model,
	}
}

func (s *contractServer) SetAgentConnection(conn *acpsdk.AgentSideConnection) {
	s.conn = conn
}

func (s *contractServer) Close() error {
	s.mu.Lock()
	ids := make([]acpsdk.SessionId, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_, _ = s.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: id})
	}
	s.model.Close()
	return nil
}

type contractModelBackend struct {
	mu          sync.Mutex
	normalCalls int
}

func (b *contractModelBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/models":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"contract-model","object":"model","created":0,"owned_by":"contract"}]}`)
	case "/responses":
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(string(body), acptest.CancelPrompt) {
			writeContractSSE(w, map[string]any{
				"type": "response.output_text.delta", "sequence_number": 1,
				"item_id": "cancel-message", "output_index": 0, "content_index": 0,
				"delta": acptest.CancelText,
			})
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}

		b.mu.Lock()
		b.normalCalls++
		call := b.normalCalls
		b.mu.Unlock()
		if call == 1 {
			writeContractSSE(w, map[string]any{
				"type": "response.output_item.done", "sequence_number": 1, "output_index": 0,
				"item": map[string]any{
					"type": "function_call", "id": "contract-function", "call_id": "contract-tool",
					"name": "glob", "arguments": `{"pattern":"*"}`, "status": "completed",
				},
			})
			writeContractSSE(w, completedEvent(2, 11, 2, 3, nil))
			return
		}
		writeContractSSE(w, map[string]any{
			"type": "response.output_text.delta", "sequence_number": 1,
			"item_id": "contract-message", "output_index": 0, "content_index": 0,
			"delta": acptest.NormalText,
		})
		writeContractSSE(w, completedEvent(2, 13, 4, 5, nil))
	default:
		http.NotFound(w, r)
	}
}

func writeContractSSE(w io.Writer, event map[string]any) {
	data, _ := json.Marshal(event)
	_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
}
