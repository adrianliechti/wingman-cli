package diff

import (
	"testing"
	"time"
)

func TestLineHunksSeparatesDistantChanges(t *testing.T) {
	before := "if\n\nh := make(header)\nh.Set(\"A\", a)\nh.Set(\"B\", b)\n"
	after := "if ready {\n\treturn nil\n}\n\nh := make(header)\nh.Set(\"A\", a)\nh.Set(\"B\", changed)\n"
	hunks := LineHunks(before, after, time.Second)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %+v, want two independent line changes", hunks)
	}
	if got := after[hunks[0].AfterStart:hunks[0].AfterEnd]; got != "if ready {\n\treturn nil\n}\n" {
		t.Fatalf("first hunk = %q", got)
	}
	if got := before[hunks[1].BeforeStart:hunks[1].BeforeEnd]; got != "h.Set(\"B\", b)\n" {
		t.Fatalf("second hunk = %q", got)
	}
}

func TestCharacterHunksSeparatesFormattingFromRename(t *testing.T) {
	before := "\tcontextBudget := sourceBudget - headerBudget\n\textra := localBudget - areaBytes\n"
	after := "contextBudget := sourceBudget - headerBudget\n\textra := contextBudget - areaBytes\n"
	hunks := CharacterHunks(before, after, time.Second)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %+v, want indentation and rename changes", hunks)
	}
	if got := before[hunks[0].BeforeStart:hunks[0].BeforeEnd]; got != "\t" {
		t.Fatalf("formatting hunk = %q", got)
	}
	if got := before[hunks[1].BeforeStart:hunks[1].BeforeEnd]; got != "local" {
		t.Fatalf("rename before = %q", got)
	}
	if got := after[hunks[1].AfterStart:hunks[1].AfterEnd]; got != "context" {
		t.Fatalf("rename after = %q", got)
	}
}

func TestCharacterHunksUseUTF8ByteOffsets(t *testing.T) {
	before, after := "value := \"😀\"", "value := \"😎\""
	hunks := CharacterHunks(before, after, time.Second)
	if len(hunks) != 1 {
		t.Fatalf("hunks = %+v", hunks)
	}
	if got := before[hunks[0].BeforeStart:hunks[0].BeforeEnd]; got != "😀" {
		t.Fatalf("before = %q", got)
	}
	if got := after[hunks[0].AfterStart:hunks[0].AfterEnd]; got != "😎" {
		t.Fatalf("after = %q", got)
	}
}
