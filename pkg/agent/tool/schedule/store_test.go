package schedule

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStoreCreatesAgentDir(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "missing", "agent"))

	if store.Exists() {
		t.Fatal("store should not report an existing task file")
	}

	err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return append(tasks, Task{
			ID:        "task-1",
			Prompt:    "run",
			Schedule:  "every 1h",
			Status:    StatusActive,
			CreatedAt: time.Now().UTC(),
		}), nil
	})
	if err != nil {
		t.Fatalf("Mutate failed: %v", err)
	}

	if !store.Exists() {
		t.Fatal("store should report the task file after a write")
	}

	got, err := store.List()
	if err != nil || len(got) != 1 || got[0].ID != "task-1" {
		t.Fatalf("List = %#v, %v, want task-1", got, err)
	}
}

func TestFileStoreReportsMalformedYAML(t *testing.T) {
	store := NewFileStore(t.TempDir())

	if err := os.WriteFile(store.Path(), []byte("tasks: ["), 0644); err != nil {
		t.Fatalf("write tasks file: %v", err)
	}

	if _, err := store.List(); err == nil {
		t.Fatal("expected malformed YAML error")
	}
}

func TestFileStoreKeepsMutateAtomicOnError(t *testing.T) {
	store := NewFileStore(t.TempDir())

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return append(tasks, Task{ID: "keep", Schedule: "every 1h", Status: StatusActive}), nil
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return nil, os.ErrInvalid
	}); err == nil {
		t.Fatal("expected the callback error")
	}

	got, _ := store.List()
	if len(got) != 1 || got[0].ID != "keep" {
		t.Fatalf("List = %#v, want the seeded task untouched", got)
	}
}

func TestMemoryStoreSignalsChanges(t *testing.T) {
	store := NewMemoryStore()

	select {
	case <-store.Changed():
		t.Fatal("no change should be signaled before the first Mutate")
	default:
	}

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return append(tasks, Task{ID: "a", Status: StatusActive}), nil
	}); err != nil {
		t.Fatalf("Mutate failed: %v", err)
	}

	select {
	case <-store.Changed():
	default:
		t.Fatal("expected a change signal after Mutate")
	}

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return nil, os.ErrInvalid
	}); err == nil {
		t.Fatal("expected the callback error")
	}

	select {
	case <-store.Changed():
		t.Fatal("a failed Mutate must not signal a change")
	default:
	}
}

func TestMemoryStoreSnapshotsAreIsolated(t *testing.T) {
	store := NewMemoryStore()

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		return append(tasks, Task{ID: "a", Status: StatusActive}), nil
	}); err != nil {
		t.Fatalf("Mutate failed: %v", err)
	}

	got, _ := store.List()
	got[0].Status = StatusPaused

	again, _ := store.List()
	if again[0].Status != StatusActive {
		t.Fatal("List must return a copy the caller cannot mutate in place")
	}

	if err := store.Mutate(func(tasks []Task) ([]Task, error) {
		tasks[0].Status = StatusPaused
		return nil, os.ErrInvalid
	}); err == nil {
		t.Fatal("expected the callback error")
	}

	final, _ := store.List()
	if final[0].Status != StatusActive {
		t.Fatal("a failed Mutate must not commit its edits")
	}
}
