package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
)

func userMsg(text string) agent.Message {
	return agent.Message{Role: agent.RoleUser, Content: []agent.Content{{Text: text}}}
}

func assistantMsg(text string) agent.Message {
	return agent.Message{Role: agent.RoleAssistant, Content: []agent.Content{{Text: text}}}
}

func fileLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := agent.State{
		Messages: []agent.Message{userMsg("hello world"), assistantMsg("hi")},
		Usage:    agent.Usage{InputTokens: 10, OutputTokens: 5},
	}

	if err := Save(dir, "s1", state); err != nil {
		t.Fatal(err)
	}

	s, err := Load(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}

	if s.ID != "s1" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Title != "hello world" {
		t.Errorf("title = %q", s.Title)
	}
	if len(s.State.Messages) != 2 {
		t.Fatalf("messages = %d", len(s.State.Messages))
	}
	if s.State.Usage != state.Usage {
		t.Errorf("usage = %+v", s.State.Usage)
	}
	if s.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}
}

func TestSaveAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")

	state := agent.State{Messages: []agent.Message{userMsg("first")}}
	if err := Save(dir, "s1", state); err != nil {
		t.Fatal(err)
	}

	state.Messages = append(state.Messages, assistantMsg("reply"), userMsg("more"))
	if err := Save(dir, "s1", state); err != nil {
		t.Fatal(err)
	}

	lines := fileLines(t, path)

	var metas, messages int
	for _, line := range lines {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid line %q: %v", line, err)
		}
		switch r.Type {
		case "meta":
			metas++
		case "event":
			if r.Event != nil && r.Event.Type == agent.EventMessage {
				messages++
			}
		}
	}

	if metas != 1 || messages != 3 {
		t.Errorf("metas=%d messages=%d", metas, messages)
	}

	s, err := Load(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.State.Messages) != 3 {
		t.Errorf("messages = %d", len(s.State.Messages))
	}
}

func TestSaveAppendsContextCheckpointWithoutRewritingHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s1.jsonl")

	state := agent.State{Messages: []agent.Message{userMsg("first"), assistantMsg("reply")}}
	if err := Save(dir, "s1", state); err != nil {
		t.Fatal(err)
	}

	state.Context = []agent.Message{userMsg("summary"), userMsg("recent")}
	state.ContextSet = true
	if err := Save(dir, "s1", state); err != nil {
		t.Fatal(err)
	}

	lines := fileLines(t, path)
	if len(lines) != 4 {
		t.Errorf("lines = %d, want 4 (meta + 2 messages + checkpoint)", len(lines))
	}

	s, err := Load(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.State.Messages) != 2 {
		t.Errorf("messages = %d", len(s.State.Messages))
	}
	if s.State.Messages[0].Content[0].Text != "first" {
		t.Errorf("first message = %q", s.State.Messages[0].Content[0].Text)
	}
	if len(s.State.Context) != 2 || s.State.Context[0].Content[0].Text != "summary" {
		t.Fatalf("context checkpoint was not restored: %#v", s.State.Context)
	}
}

func TestLegacyMigration(t *testing.T) {
	dir := t.TempDir()

	legacy := Session{
		ID:        "old",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		State: agent.State{
			Messages: []agent.Message{userMsg("legacy message")},
			Usage:    agent.Usage{InputTokens: 1},
		},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "old.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.State.Messages) != 1 {
		t.Fatalf("messages = %d", len(s.State.Messages))
	}

	s.State.Messages = append(s.State.Messages, assistantMsg("reply"))
	if err := Save(dir, "old", s.State); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "old.json")); !os.IsNotExist(err) {
		t.Error("legacy file not removed")
	}

	migrated, err := Load(dir, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated.State.Messages) != 2 {
		t.Errorf("messages = %d", len(migrated.State.Messages))
	}
	if migrated.CreatedAt.Sub(legacy.CreatedAt).Abs() > time.Second {
		t.Errorf("created_at not preserved: %v != %v", migrated.CreatedAt, legacy.CreatedAt)
	}
}

func TestLegacyJSONLMigrationIsolatedAndOneWay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old-lines.jsonl")
	created := time.Now().Add(-2 * time.Hour).UTC().Round(time.Second)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	message := userMsg("legacy jsonl")
	usage := agent.Usage{InputTokens: 7}
	for _, r := range []legacyRecord{
		{Type: "meta", ID: "old-lines", CreatedAt: &created},
		{Type: "message", Message: &message},
		{Type: "state", Usage: &usage},
	} {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir, "old-lines")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.State.Messages) != 1 || loaded.State.Usage.InputTokens != 7 {
		t.Fatalf("migrated state = %#v", loaded.State)
	}
	if loaded.CreatedAt.Sub(created).Abs() > time.Second {
		t.Fatalf("created_at = %v, want %v", loaded.CreatedAt, created)
	}

	lines := fileLines(t, path)
	for i, line := range lines {
		var r record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if r.Type != "meta" || r.Version != journalVersion {
				t.Fatalf("migrated header = %#v", r)
			}
			continue
		}
		if r.Type != "event" || r.Event == nil {
			t.Fatalf("legacy record survived migration: %#v", r)
		}
	}
}

func TestOpenJournalRepairsIncompleteFinalRecord(t *testing.T) {
	dir := t.TempDir()
	state := agent.State{Messages: []agent.Message{userMsg("first")}}
	if err := Save(dir, "repair", state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "repair.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"event","event":`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	journalCoordinators.Delete(filepath.Clean(path)) // simulate a new process
	j, err := OpenJournal(dir, "repair")
	if err != nil {
		t.Fatal(err)
	}
	message := assistantMsg("second")
	if err := j.AppendEvents([]agent.RuntimeEvent{{
		Sequence: 2, ID: "second", Type: agent.EventMessage, At: time.Now().UTC(), Message: &message,
	}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir, "repair")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.State.Messages) != 2 || loaded.State.Messages[1].Content[0].Text != "second" {
		t.Fatalf("repaired messages = %#v", loaded.State.Messages)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "a", agent.State{Messages: []agent.Message{userMsg("session a")}}); err != nil {
		t.Fatal(err)
	}

	legacy := Session{ID: "b", Title: "session b", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	legacy.State.Messages = []agent.Message{userMsg("session b")}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "b.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	sessions, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d", len(sessions))
	}

	titles := map[string]string{}
	for _, s := range sessions {
		titles[s.ID] = s.Title
	}
	if titles["a"] != "session a" || titles["b"] != "session b" {
		t.Errorf("titles = %v", titles)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "s1", agent.State{Messages: []agent.Message{userMsg("hi")}}); err != nil {
		t.Fatal(err)
	}

	if err := Delete(dir, "s1"); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir, "s1"); err == nil {
		t.Error("expected error loading deleted session")
	}

	if err := Save(dir, "s1", agent.State{Messages: []agent.Message{userMsg("again")}}); err != nil {
		t.Fatal(err)
	}

	s, err := Load(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.State.Messages) != 1 {
		t.Errorf("messages = %d", len(s.State.Messages))
	}
}

func TestSessionIDCannotEscapeStorageDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", ".", "..", "../outside", `..\\outside`, "/absolute"} {
		t.Run(strings.ReplaceAll(id, "/", "_"), func(t *testing.T) {
			if _, err := ArtifactDir(dir, id); err == nil {
				t.Fatalf("ArtifactDir accepted %q", id)
			}
			if _, err := OpenJournal(dir, id); err == nil {
				t.Fatalf("OpenJournal accepted %q", id)
			}
			if err := Save(dir, id, agent.State{Messages: []agent.Message{userMsg("unsafe")}}); err == nil {
				t.Fatalf("Save accepted %q", id)
			}
			if _, err := Load(dir, id); err == nil {
				t.Fatalf("Load accepted %q", id)
			}
			if err := Delete(dir, id); err == nil {
				t.Fatalf("Delete accepted %q", id)
			}
		})
	}
}
