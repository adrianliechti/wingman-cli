package tracker

import (
	"os"
	"testing"
	"time"
)

func testTracker(t *testing.T) *Tracker {
	t.Helper()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	return New(root)
}

func TestRememberAndGet(t *testing.T) {
	tr := testTracker(t)
	now := time.Now()

	tr.Remember("file.go", "package main", now, false)

	snap, ok := tr.Get("file.go")
	if !ok {
		t.Fatal("expected to find file.go")
	}

	if snap.Content != "package main" {
		t.Errorf("expected 'package main', got %q", snap.Content)
	}

	if snap.Partial {
		t.Error("expected Partial to be false")
	}
}

func TestGet_NotFound(t *testing.T) {
	tr := testTracker(t)

	_, ok := tr.Get("missing.go")
	if ok {
		t.Error("expected missing file to not be found")
	}
}

func TestRemember_Overwrites(t *testing.T) {
	tr := testTracker(t)
	now := time.Now()

	tr.Remember("file.go", "v1", now, false)
	tr.Remember("file.go", "v2", now.Add(time.Second), true)

	snap, _ := tr.Get("file.go")
	if snap.Content != "v2" {
		t.Errorf("expected 'v2', got %q", snap.Content)
	}
	if !snap.Partial {
		t.Error("expected Partial to be true after overwrite")
	}
}

func TestClear(t *testing.T) {
	tr := testTracker(t)
	now := time.Now()

	tr.Remember("a.go", "a", now, false)
	tr.Remember("b.go", "b", now, false)

	tr.Clear()

	if _, ok := tr.Get("a.go"); ok {
		t.Error("expected a.go to be cleared")
	}
	if _, ok := tr.Get("b.go"); ok {
		t.Error("expected b.go to be cleared")
	}
}

func TestNilTracker(t *testing.T) {
	var tr *Tracker

	// Should not panic
	tr.Remember("file.go", "content", time.Now(), false)
	tr.Clear()

	_, ok := tr.Get("file.go")
	if ok {
		t.Error("nil tracker should return not-found")
	}
}
