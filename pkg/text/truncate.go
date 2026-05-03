// Package text provides small string utilities shared across the codebase.
package text

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TruncateMiddle returns s unchanged when its byte length fits within
// maxBytes. Otherwise it keeps a head and a tail of the input, joined by a
// "…N chars truncated…" marker, with the byte budget split evenly between
// head and tail at UTF-8 boundaries. The returned string may exceed
// maxBytes by the marker length.
func TruncateMiddle(s string, maxBytes int) string {
	if s == "" {
		return ""
	}

	totalChars := utf8.RuneCountInString(s)

	if maxBytes <= 0 {
		return formatTruncationMarker(totalChars)
	}

	if len(s) <= maxBytes {
		return s
	}

	leftBudget, rightBudget := splitBudget(maxBytes)
	removedChars, head, tail := splitString(s, leftBudget, rightBudget)
	marker := formatTruncationMarker(removedChars)

	var b strings.Builder
	b.Grow(len(head) + len(marker) + len(tail))
	b.WriteString(head)
	b.WriteString(marker)
	b.WriteString(tail)
	return b.String()
}

func splitBudget(budget int) (left, right int) {
	left = budget / 2
	return left, budget - left
}

// splitString picks a prefix whose byte-end is <= beginningBytes and a
// suffix whose byte-start is >= len(s)-endBytes, both at UTF-8 boundaries.
// Returns the count of dropped chars and slices into s.
func splitString(s string, beginningBytes, endBytes int) (removedChars int, head, tail string) {
	if s == "" {
		return 0, "", ""
	}

	length := len(s)
	tailStartTarget := max(length-endBytes, 0)

	prefixEnd := 0
	suffixStart := length
	suffixStarted := false

	for idx, r := range s {
		charEnd := idx + utf8.RuneLen(r)
		if charEnd <= beginningBytes {
			prefixEnd = charEnd
			continue
		}

		if idx >= tailStartTarget {
			if !suffixStarted {
				suffixStart = idx
				suffixStarted = true
			}
			continue
		}

		removedChars++
	}

	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}

	return removedChars, s[:prefixEnd], s[suffixStart:]
}

func formatTruncationMarker(removedChars int) string {
	return fmt.Sprintf("…%d chars truncated…", removedChars)
}
