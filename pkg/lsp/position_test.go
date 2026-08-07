package lsp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestPositionFromDisplay(t *testing.T) {
	path := writeTempFile(t, "package main\n\nfunc main() {}\n")

	pos, err := PositionFromDisplay(path, 3, 6)
	if err != nil {
		t.Fatalf("PositionFromDisplay: %v", err)
	}
	if pos.Line != 2 || pos.Character != 5 {
		t.Fatalf("pos = %+v, want line 2 char 5", pos)
	}
}

func TestPositionFromDisplayOutOfRange(t *testing.T) {
	path := writeTempFile(t, "package main\n")

	_, err := PositionFromDisplay(path, 99, 1)
	if err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("expected out of range error, got: %v", err)
	}
}

func TestPositionFromDisplayConvertsToUTF16(t *testing.T) {
	path := writeTempFile(t, "x := \"\U0001F600\" + name\n")

	pos, err := PositionFromDisplay(path, 1, 12)
	if err != nil {
		t.Fatalf("PositionFromDisplay: %v", err)
	}
	if pos.Character != 12 {
		t.Fatalf("char = %d, want 12 (emoji counts as two UTF-16 units)", pos.Character)
	}
}

func TestPositionOfSymbolOnLine(t *testing.T) {
	path := writeTempFile(t, "package main\n\nfunc mainHelper() { main() }\n")

	pos, err := PositionOfSymbolOnLine(path, 3, "main")
	if err != nil {
		t.Fatalf("PositionOfSymbolOnLine: %v", err)
	}
	if pos.Line != 2 || pos.Character != 20 {
		t.Fatalf("pos = %+v, want line 2 char 20 (word-boundary match, not mainHelper)", pos)
	}

	if _, err := PositionOfSymbolOnLine(path, 1, "missing"); err == nil || !strings.Contains(err.Error(), "not found on line") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

func TestPositionOfSymbol(t *testing.T) {
	path := writeTempFile(t, "package main\n\nvar counter int\n\nfunc run() { counter++ }\n")

	pos, ok := PositionOfSymbol(path, "counter")
	if !ok {
		t.Fatal("expected symbol to be found")
	}
	if pos.Line != 2 || pos.Character != 4 {
		t.Fatalf("pos = %+v, want first occurrence at line 2 char 4", pos)
	}

	if _, ok := PositionOfSymbol(path, "absent"); ok {
		t.Fatal("expected symbol to be absent")
	}
}
