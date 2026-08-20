package diff

import (
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// Hunk describes one contiguous change using UTF-8 byte offsets.
type Hunk struct {
	BeforeStart int
	BeforeEnd   int
	AfterStart  int
	AfterEnd    int
}

// LineHunks aligns complete lines before returning changed ranges.
func LineHunks(before, after string, timeout time.Duration) []Hunk {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = timeout
	beforeLines, afterLines, lines := dmp.DiffLinesToChars(before, after)
	diffs := dmp.DiffMain(beforeLines, afterLines, false)
	return hunks(dmp.DiffCharsToLines(diffs, lines))
}

// CharacterHunks returns semantically cleaned character-level changes.
func CharacterHunks(before, after string, timeout time.Duration) []Hunk {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = timeout
	diffs := dmp.DiffMain(before, after, false)
	return hunks(dmp.DiffCleanupSemantic(diffs))
}

func hunks(diffs []diffmatchpatch.Diff) []Hunk {
	result := make([]Hunk, 0, 2)
	beforeOffset, afterOffset := 0, 0
	var current *Hunk
	flush := func() {
		if current != nil {
			result = append(result, *current)
			current = nil
		}
	}
	start := func() {
		if current == nil {
			current = &Hunk{
				BeforeStart: beforeOffset,
				BeforeEnd:   beforeOffset,
				AfterStart:  afterOffset,
				AfterEnd:    afterOffset,
			}
		}
	}
	for _, item := range diffs {
		switch item.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			beforeOffset += len(item.Text)
			afterOffset += len(item.Text)
		case diffmatchpatch.DiffDelete:
			start()
			beforeOffset += len(item.Text)
			current.BeforeEnd = beforeOffset
		case diffmatchpatch.DiffInsert:
			start()
			afterOffset += len(item.Text)
			current.AfterEnd = afterOffset
		}
	}
	flush()
	return result
}
