package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
)

func positionTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.go")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPositionFromDisplay(t *testing.T) {
	position, err := positionFromDisplay(positionTestFile(t, "package main\n\nfunc main() {}\n"), 3, 6)
	if err != nil || position.Line != 2 || position.Character != 5 {
		t.Fatalf("position=%+v err=%v", position, err)
	}
}

func TestPositionFromDisplayConvertsUTF16(t *testing.T) {
	position, err := positionFromDisplay(positionTestFile(t, "x := \"\U0001F600\" + name\n"), 1, 12)
	if err != nil || position.Character != 12 {
		t.Fatalf("position=%+v err=%v", position, err)
	}
}

func TestPositionOfSymbolOnLine(t *testing.T) {
	path := positionTestFile(t, "package main\n\nfunc mainHelper() { main() }\n")
	position, err := positionOfSymbolOnLine(path, 3, "main")
	if err != nil || position.Line != 2 || position.Character != 20 {
		t.Fatalf("position=%+v err=%v", position, err)
	}
	if _, err := positionOfSymbolOnLine(path, 1, "missing"); err == nil || !strings.Contains(err.Error(), "not found on line") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestMatchSymbol(t *testing.T) {
	candidates := []symbolCandidate{
		{name: "Manager", qualified: "Manager", position: lsp.Position{Line: 10}},
		{name: "Close", qualified: "Manager.Close", position: lsp.Position{Line: 20}},
		{name: "(*Session).Close", qualified: "(*Session).Close", position: lsp.Position{Line: 30}},
	}
	for query, line := range map[string]uint32{"Manager": 10, "Close": 20, "Manager.Close": 20, "Session.Close": 30} {
		candidate, ok := matchSymbol(candidates, query)
		if !ok || candidate.position.Line != line {
			t.Fatalf("%s: candidate=%+v ok=%v", query, candidate, ok)
		}
	}
	if _, ok := matchSymbol(candidates, "Missing"); ok {
		t.Fatal("missing symbol matched")
	}
}
