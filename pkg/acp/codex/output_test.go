package codex

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToolOutputKeepsABoundedUTF8Tail(t *testing.T) {
	var output toolOutput
	output.append(strings.Repeat("界", maxToolOutputBytes/3))
	for range 100 {
		output.append(strings.Repeat("界", 4096))
	}
	output.append("last line")
	got := output.String()
	if output.data.Len() > maxToolOutputBytes || len(got) > maxToolOutputBytes+100 {
		t.Fatalf("tool output grew beyond its limit: %d bytes", len(got))
	}
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "[Output truncated;") || !strings.HasSuffix(got, "last line") {
		t.Fatal("truncated output lost its marker, UTF-8 boundary, or final output")
	}
}

func TestLargeAggregatedOutputIsBoundedToo(t *testing.T) {
	got := boundedToolOutput(strings.Repeat("x", 2*maxToolOutputBytes) + "end")
	if len(got) > maxToolOutputBytes+100 || !strings.HasPrefix(got, "[Output truncated;") || !strings.HasSuffix(got, "end") {
		t.Fatalf("aggregated output was not bounded: %d bytes", len(got))
	}
}
