package shell

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestCappedBufferKeepsInlineOutputWithoutScratchDir(t *testing.T) {
	var b cappedBuffer

	chunk := strings.Repeat("x", 1024*1024)
	for range 20 {
		n, err := b.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("write returned %d, %v", n, err)
		}
	}

	result := b.result()
	if b.buf.Len() != maxOutputBytes {
		t.Fatalf("inline buffer = %d bytes", b.buf.Len())
	}
	dropped := 4 * 1024 * 1024
	if !strings.Contains(result, fmt.Sprintf("[output exceeded 16MB; %d trailing bytes dropped (no scratch directory for a full transcript)]", dropped)) {
		t.Fatalf("missing drop notice, got tail: %q", result[len(result)-120:])
	}
	if !strings.HasPrefix(result, "xxxx") || len(result) < maxOutputBytes {
		t.Fatalf("inline output not retained: %d bytes", len(result))
	}
}

func TestCappedBufferCapsSpillSize(t *testing.T) {
	b := newCappedBuffer(t.TempDir())
	b.spillLimit = maxOutputBytes + 1024*1024

	b.Write([]byte(strings.Repeat("a", maxOutputBytes)))
	b.Write([]byte(strings.Repeat("b", 2*1024*1024)))

	result := b.result()
	if b.dropped != 1024*1024 {
		t.Fatalf("dropped = %d", b.dropped)
	}
	content, err := os.ReadFile(b.spillPath)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(content)) != b.spillLimit {
		t.Fatalf("transcript = %d bytes", len(content))
	}
	if !strings.Contains(result, fmt.Sprintf("first %d bytes of raw output saved to %s", b.spillLimit, b.spillPath)) {
		t.Fatalf("missing partial transcript notice, got tail: %q", result[len(result)-200:])
	}
}

func TestCappedBufferSmallOutputUntouched(t *testing.T) {
	b := newCappedBuffer(t.TempDir())
	b.Write([]byte("hello"))

	if got := b.result(); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if b.spillPath != "" {
		t.Fatalf("small output created transcript %q", b.spillPath)
	}
}

func TestCappedBufferRetainsTailAndSpillsCompleteOutput(t *testing.T) {
	dir := t.TempDir()
	b := newCappedBuffer(dir)
	b.Write([]byte(strings.Repeat("a", maxOutputBytes)))
	b.Write([]byte("FINAL DIAGNOSTIC"))

	result := b.result()
	if !strings.Contains(result, "FINAL DIAGNOSTIC") {
		t.Fatalf("final diagnostic was lost: %q", result[len(result)-200:])
	}
	if b.spillPath == "" {
		t.Fatal("expected complete output transcript path")
	}
	content, err := os.ReadFile(b.spillPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != maxOutputBytes+len("FINAL DIAGNOSTIC") || !strings.HasSuffix(string(content), "FINAL DIAGNOSTIC") {
		t.Fatalf("transcript incomplete: %d bytes", len(content))
	}
}

func TestProgressBufferReportsLastCompleteLine(t *testing.T) {
	var reported []string
	b := &progressBuffer{report: func(line string) { reported = append(reported, line) }}

	b.Write([]byte("compiling foo.go\ncompiling ba"))
	b.Write([]byte("r.go\n"))

	if len(reported) == 0 {
		t.Fatal("expected a progress report")
	}
	if got := reported[0]; got != "compiling foo.go" {
		t.Fatalf("first report = %q", got)
	}

	if got := b.result(); got != "compiling foo.go\ncompiling bar.go\n" {
		t.Fatalf("result = %q", got)
	}
}

func TestProgressBufferSkipsBlankLines(t *testing.T) {
	var reported []string
	b := &progressBuffer{report: func(line string) { reported = append(reported, line) }}

	b.Write([]byte("real output\n\n   \n"))

	if len(reported) != 1 || reported[0] != "real output" {
		t.Fatalf("reported = %v", reported)
	}
}

func TestProgressBufferNilReport(t *testing.T) {
	b := &progressBuffer{}
	b.Write([]byte("output\n"))

	if got := b.result(); got != "output\n" {
		t.Fatalf("result = %q", got)
	}
}

func TestSanitizeOutputStripsEscapes(t *testing.T) {
	in := "\x1b[?2026h\x1b[?25l\x1b[22;1H\x1b[2KRun these\x1b[0m\nplain"
	if got := sanitizeOutput(in); got != "Run these\nplain" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeOutputResolvesCarriageReturns(t *testing.T) {
	if got := sanitizeOutput("progress 50%\rprogress 100%\ndone\r\n"); got != "progress 100%\ndone\n" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeOutput("spinner |\r"); got != "spinner |" {
		t.Fatalf("trailing CR: got %q", got)
	}
}

func TestSanitizeOutputPlainPassthrough(t *testing.T) {
	if got := sanitizeOutput("ok\ttabs kept\nsecond"); got != "ok\ttabs kept\nsecond" {
		t.Fatalf("got %q", got)
	}
}
