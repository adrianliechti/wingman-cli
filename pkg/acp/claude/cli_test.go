package claude

import (
	"strings"
	"testing"
)

func TestCLIScannerAcceptsLargeDocumentRecord(t *testing.T) {
	line := `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"` + strings.Repeat("A", 9*1024*1024) + `"}}]}]}}`
	scanner := newCLIScanner(strings.NewReader(line + "\n"))
	if !scanner.Scan() {
		t.Fatalf("large record was rejected: %v", scanner.Err())
	}
	if got := len(scanner.Bytes()); got != len(line) {
		t.Fatalf("record length = %d, want %d", got, len(line))
	}
	if scanner.Scan() {
		t.Fatal("scanner returned an unexpected second record")
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
