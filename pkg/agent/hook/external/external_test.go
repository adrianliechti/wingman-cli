package external

import (
	"io"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCappedBufferDrainsAndMarksTruncation(t *testing.T) {
	var output cappedBuffer
	output.limit = maxHookOutput + 1

	input := strings.Repeat("x", maxHookOutput*3)
	written, err := output.Write([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if written != len(input) {
		t.Fatalf("Write returned %d bytes, want %d", written, len(input))
	}
	if output.Len() != maxHookOutput+1 {
		t.Fatalf("retained %d bytes, want %d", output.Len(), maxHookOutput+1)
	}
	if got := limit(output.String()); !strings.HasSuffix(got, "\n[hook output truncated]") {
		t.Fatalf("limited output did not report truncation: %q", got)
	}
}

func TestBoundAdditionalContextUsesDefaultAndConfiguredLimits(t *testing.T) {
	short := strings.Repeat("x", defaultAdditionalContextTokens*4)
	if got := boundAdditionalContext(short, nil); got != short {
		t.Fatal("default limit changed an in-budget context")
	}

	limit := 100
	input := "HEAD" + strings.Repeat("☃", 1_000) + "TAIL"
	got := boundAdditionalContext(input, &limit)
	if !strings.Contains(got, "additionalContext truncated") {
		t.Fatal("configured limit did not report truncation")
	}
	if !strings.Contains(got, "HEAD") || !strings.Contains(got, "TAIL") {
		t.Fatal("configured limit did not preserve the context head and tail")
	}
	if !utf8.ValidString(got) {
		t.Fatal("configured limit produced invalid UTF-8")
	}

	unlimited := 0
	if got := boundAdditionalContext(input, &unlimited); got != input {
		t.Fatal("zero limit did not preserve unlimited context")
	}
}

func TestCappedBufferLimitsExecStyleCopy(t *testing.T) {
	output := cappedBuffer{limit: maxHookOutput + 1}

	n, err := io.Copy(&output, strings.NewReader(strings.Repeat("x", maxHookOutput*3)))
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(maxHookOutput*3) {
		t.Fatalf("io.Copy reported %d bytes, want %d", n, maxHookOutput*3)
	}
	if output.Len() != maxHookOutput+1 {
		t.Fatalf("retained %d bytes through io.Copy, want %d", output.Len(), maxHookOutput+1)
	}
	if _, ok := any(&output).(io.ReaderFrom); ok {
		t.Fatal("cappedBuffer exposes ReaderFrom, which bypasses the cap in os/exec")
	}
}
