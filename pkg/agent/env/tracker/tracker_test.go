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

	tr.Remember("file.go", "package main", now, 0, 0)

	snap, ok := tr.Get("file.go")
	if !ok {
		t.Fatal("expected to find file.go")
	}

	if snap.Content != "package main" {
		t.Errorf("expected 'package main', got %q", snap.Content)
	}

	if snap.IsPartial() {
		t.Error("full read should not be partial")
	}
}

func TestPartialRead(t *testing.T) {
	tr := testTracker(t)
	now := time.Now()

	tr.Remember("file.go", "package main", now, 10, 50)

	snap, _ := tr.Get("file.go")

	if !snap.IsPartial() {
		t.Error("read with offset/limit should be partial")
	}

	if snap.Offset != 10 {
		t.Errorf("expected offset 10, got %d", snap.Offset)
	}

	if snap.Limit != 50 {
		t.Errorf("expected limit 50, got %d", snap.Limit)
	}
}

func TestPartialRead_OffsetOnly(t *testing.T) {
	tr := testTracker(t)

	tr.Remember("file.go", "content", time.Now(), 5, 0)

	snap, _ := tr.Get("file.go")
	if !snap.IsPartial() {
		t.Error("read with offset should be partial")
	}
}

func TestPartialRead_LimitOnly(t *testing.T) {
	tr := testTracker(t)

	tr.Remember("file.go", "content", time.Now(), 0, 100)

	snap, _ := tr.Get("file.go")
	if !snap.IsPartial() {
		t.Error("read with limit should be partial")
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

	tr.Remember("file.go", "v1", now, 0, 0)
	tr.Remember("file.go", "v2", now.Add(time.Second), 10, 20)

	snap, _ := tr.Get("file.go")
	if snap.Content != "v2" {
		t.Errorf("expected 'v2', got %q", snap.Content)
	}
	if !snap.IsPartial() {
		t.Error("expected partial after overwrite with offset/limit")
	}
}

func TestClear(t *testing.T) {
	tr := testTracker(t)
	now := time.Now()

	tr.Remember("a.go", "a", now, 0, 0)
	tr.Remember("b.go", "b", now, 0, 0)

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
	tr.Remember("file.go", "content", time.Now(), 0, 0)
	tr.Clear()

	_, ok := tr.Get("file.go")
	if ok {
		t.Error("nil tracker should return not-found")
	}
}
