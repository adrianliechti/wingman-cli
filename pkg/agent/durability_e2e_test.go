package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/session"
)

const doneEvent = "data: [DONE]\n\n"

func completedText(text string) string {
	return fmt.Sprintf("data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"output\":[{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":%q,\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n", text) + doneEvent
}

func completedToolCall(call, name string) string {
	return fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"sequence_number\":1,\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_%s\",\"call_id\":\"%s\",\"name\":%q,\"arguments\":\"{}\",\"status\":\"completed\"}}\n\n", call, call, name) +
		"data: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"usage\":{\"input_tokens\":1,\"input_tokens_details\":{\"cached_tokens\":0},\"output_tokens\":1}}}\n\n" + doneEvent
}

func scriptedProvider(t *testing.T, bodies ...string) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := int(requests.Add(1)) - 1
		if index >= len(bodies) {
			index = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, bodies[index])
	}))
	t.Cleanup(server.Close)
	t.Setenv("WINGMAN_URL", server.URL)
}

func e2eConfig(t *testing.T, tools ...tool.Tool) *agent.Config {
	t.Helper()
	cfg, err := agent.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = func() string { return "test-model" }
	cfg.Instructions = func() string { return "test" }
	cfg.Tools = func() []tool.Tool { return tools }
	return cfg
}

func drain(t *testing.T, a *agent.Agent, text string) error {
	t.Helper()
	stream, err := a.Send(t.Context(), []agent.Content{{Text: text}})
	if err != nil {
		return err
	}
	for _, err := range stream {
		if err != nil {
			return err
		}
	}
	return nil
}

func eventsOfType(state agent.State, kind agent.RuntimeEventType) []agent.RuntimeEvent {
	var found []agent.RuntimeEvent
	for _, event := range state.Events {
		if event.Type == kind {
			found = append(found, event)
		}
	}
	return found
}

func TestCrashDuringMutatingToolRestoresUncertainOutcome(t *testing.T) {
	sessionsDir := t.TempDir()
	const sessionID = "crash-e2e"

	scriptedProvider(t, completedText("first answer"), completedToolCall("call_1", "mutate"))

	journal, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	toolEntered := make(chan struct{})
	crashCtx, crash := context.WithCancel(context.Background())
	t.Cleanup(crash)

	var once sync.Once
	var dead atomic.Bool
	recorder := agent.EventRecorderFunc(func(events []agent.RuntimeEvent) error {
		if dead.Load() {
			return nil
		}
		for _, event := range events {
			if event.Type == agent.EventToolStarted {
				if err := journal.AppendEvents(events); err != nil {
					return err
				}
				dead.Store(true)
				once.Do(func() { close(toolEntered) })
				return nil
			}
		}
		return journal.AppendEvents(events)
	})

	live := &agent.Agent{Config: e2eConfig(t, tool.Tool{
		Name:        "mutate",
		Description: "writes something",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Effect:      tool.StaticEffect(tool.EffectMutates),
		Execute: func(ctx context.Context, _ map[string]any) (tool.Result, error) {
			<-ctx.Done()
			return tool.Result{}, ctx.Err()
		},
	}), Recorder: recorder}

	if err := drain(t, live, "hello"); err != nil {
		t.Fatalf("first turn: %v", err)
	}

	go func() {
		<-toolEntered
		crash()
	}()

	stream, err := live.Send(crashCtx, []agent.Content{{Text: "now mutate"}})
	if err == nil {
		for _, streamErr := range stream {
			if streamErr != nil {
				break
			}
		}
	}

	select {
	case <-toolEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("mutating tool never started")
	}

	loaded, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	restored := &agent.Agent{Config: e2eConfig(t), Recorder: reopened}
	if err := restored.Restore(loaded.State); err != nil {
		t.Fatal(err)
	}

	if openTerminals := eventsOfType(loaded.State, agent.EventToolTerminal); len(openTerminals) != 0 {
		t.Fatalf("crashed tool should have no terminal fact on disk, got %d", len(openTerminals))
	}

	if err := restored.ReconcileInterrupted("process stopped"); err != nil {
		t.Fatal(err)
	}

	terminals := eventsOfType(restored.StateSnapshot(), agent.EventToolTerminal)
	if len(terminals) != 1 {
		t.Fatalf("want exactly one reconciled tool terminal, got %d", len(terminals))
	}
	if got := terminals[0].Terminal; got == nil || got.Status != agent.RuntimeInterrupted || !got.OutcomeUncertain {
		t.Fatalf("mutating tool terminal = %#v, want interrupted with uncertain outcome", got)
	}

	turnTerminals := eventsOfType(restored.StateSnapshot(), agent.EventTurnTerminal)
	if len(turnTerminals) == 0 {
		t.Fatal("interrupted turn was never closed")
	}
	last := turnTerminals[len(turnTerminals)-1]
	if last.Terminal == nil || last.Terminal.Status != agent.RuntimeInterrupted {
		t.Fatalf("last turn terminal = %#v, want interrupted", last.Terminal)
	}

	var texts []string
	for _, m := range restored.MessagesSnapshot() {
		for _, c := range m.Content {
			if c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "hello") || !strings.Contains(joined, "first answer") {
		t.Fatalf("pre-crash history lost, messages = %q", joined)
	}

	after, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsOfType(after.State, agent.EventToolTerminal)) != 1 {
		t.Fatal("reconciled terminal was not persisted to the journal")
	}
}

func TestRestartDoesNotDuplicateLedgerEvents(t *testing.T) {
	sessionsDir := t.TempDir()
	const sessionID = "restart-e2e"

	scriptedProvider(t, completedText("answer one"), completedText("answer two"))

	journal, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	first := &agent.Agent{Config: e2eConfig(t), Recorder: journal}
	if err := drain(t, first, "one"); err != nil {
		t.Fatal(err)
	}

	loaded, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	before := len(loaded.State.Events)
	if before == 0 {
		t.Fatal("first turn recorded nothing")
	}

	if err := session.Save(sessionsDir, sessionID, loaded.State); err != nil {
		t.Fatal(err)
	}

	reopened, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	second := &agent.Agent{Config: e2eConfig(t), Recorder: reopened}
	if err := second.Restore(loaded.State); err != nil {
		t.Fatal(err)
	}
	if err := drain(t, second, "two"); err != nil {
		t.Fatal(err)
	}

	final, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}
	var sequence uint64
	for _, event := range final.State.Events {
		seen[event.ID]++
		if event.Sequence <= sequence {
			t.Fatalf("sequence went backwards at %s: %d after %d", event.ID, event.Sequence, sequence)
		}
		sequence = event.Sequence
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("event %s persisted %d times", id, count)
		}
	}
	if len(final.State.Events) <= before {
		t.Fatalf("second turn added no events (%d then %d)", before, len(final.State.Events))
	}

	var texts []string
	for _, m := range final.State.Messages {
		for _, c := range m.Content {
			if c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"one", "answer one", "two", "answer two"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in restored history %q", want, joined)
		}
	}
}

func TestJournalFailureKeepsAgentAndDiskConsistent(t *testing.T) {
	sessionsDir := t.TempDir()
	const sessionID = "recorder-failure-e2e"

	scriptedProvider(t, completedText("answer"))

	journal, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	var fail atomic.Bool
	recorder := agent.EventRecorderFunc(func(events []agent.RuntimeEvent) error {
		if fail.Load() {
			return errors.New("disk full")
		}
		return journal.AppendEvents(events)
	})

	a := &agent.Agent{Config: e2eConfig(t), Recorder: recorder}
	if err := drain(t, a, "before failure"); err != nil {
		t.Fatal(err)
	}

	healthy := len(a.StateSnapshot().Events)
	fail.Store(true)
	if err := drain(t, a, "during failure"); err == nil {
		t.Fatal("expected the run to surface the recorder failure")
	}

	if got := len(a.StateSnapshot().Events); got != healthy {
		t.Fatalf("agent kept %d events after a rejected append, want %d", got, healthy)
	}

	onDisk, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk.State.Events) != healthy {
		t.Fatalf("journal holds %d events, agent holds %d", len(onDisk.State.Events), healthy)
	}
}

func TestMigratedLegacySessionAcceptsNewTurns(t *testing.T) {
	sessionsDir := t.TempDir()
	const sessionID = "legacy-e2e"

	legacy := map[string]any{
		"id":         sessionID,
		"created_at": time.Now().Add(-time.Hour).UTC(),
		"updated_at": time.Now().Add(-time.Hour).UTC(),
		"state": map[string]any{
			"messages": []map[string]any{
				{"role": "user", "content": []map[string]any{{"text": "legacy question"}}},
				{"role": "assistant", "content": []map[string]any{{"text": "legacy answer"}}},
			},
			"usage": map[string]any{"input_tokens": 7},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, sessionID+".json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	scriptedProvider(t, completedText("post-migration answer"))

	loaded, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatalf("migrate legacy session: %v", err)
	}
	if len(loaded.State.Messages) != 2 {
		t.Fatalf("migrated messages = %#v", loaded.State.Messages)
	}

	journal, err := session.OpenJournal(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{Config: e2eConfig(t), Recorder: journal}
	if err := a.Restore(loaded.State); err != nil {
		t.Fatal(err)
	}
	if err := drain(t, a, "after migration"); err != nil {
		t.Fatalf("turn on a migrated session: %v", err)
	}

	final, err := session.Load(sessionsDir, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	var sequence uint64
	seen := map[string]int{}
	for _, event := range final.State.Events {
		seen[event.ID]++
		if event.Sequence <= sequence {
			t.Fatalf("migrated journal is not strictly ordered at %s", event.ID)
		}
		sequence = event.Sequence
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("event %s persisted %d times after migration", id, count)
		}
	}

	var texts []string
	for _, m := range final.State.Messages {
		for _, c := range m.Content {
			if c.Text != "" {
				texts = append(texts, c.Text)
			}
		}
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"legacy question", "legacy answer", "after migration", "post-migration answer"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in post-migration history %q", want, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, sessionID+".json")); !os.IsNotExist(err) {
		t.Fatal("legacy file survived migration")
	}
}
