package ansi

import (
	"strings"

	"github.com/rivo/uniseg"
)

type segment struct {
	text  string
	width int
	state string
}

// parse splits styled text into grapheme segments, tracking the accumulated
// SGR state active before each one. Non-SGR escape sequences are dropped.
type parser struct {
	state    string
	segments []segment
}

func isReset(seq string) bool {
	return seq == "\x1b[m" || seq == "\x1b[0m"
}

func (p *parser) feed(text string) {
	for len(text) > 0 {
		if text[0] == 0x1b {
			seq, rest := readEscape(text)
			if strings.HasSuffix(seq, "m") && strings.HasPrefix(seq, "\x1b[") {
				if isReset(seq) {
					p.state = ""
				} else {
					p.state += seq
				}
			}
			text = rest
			continue
		}

		gr := uniseg.NewGraphemes(text)
		if !gr.Next() {
			break
		}

		cluster := gr.Str()
		p.segments = append(p.segments, segment{
			text:  cluster,
			width: gr.Width(),
			state: p.state,
		})
		text = text[len(cluster):]
	}
}

func readEscape(text string) (string, string) {
	if len(text) < 2 {
		return text, ""
	}

	switch text[1] {
	case '[':
		for i := 2; i < len(text); i++ {
			b := text[i]
			if b >= 0x40 && b <= 0x7e {
				return text[:i+1], text[i+1:]
			}
		}
		return text, ""
	case ']':
		for i := 2; i < len(text); i++ {
			if text[i] == 0x07 {
				return text[:i+1], text[i+1:]
			}
			if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
				return text[:i+2], text[i+2:]
			}
		}
		return text, ""
	default:
		return text[:2], text[2:]
	}
}

func Strip(text string) string {
	var sb strings.Builder
	for len(text) > 0 {
		if text[0] == 0x1b {
			_, rest := readEscape(text)
			text = rest
			continue
		}
		idx := strings.IndexByte(text, 0x1b)
		if idx < 0 {
			sb.WriteString(text)
			break
		}
		sb.WriteString(text[:idx])
		text = text[idx:]
	}
	return sb.String()
}

func Width(text string) int {
	if !strings.ContainsRune(text, 0x1b) {
		return uniseg.StringWidth(text)
	}
	return uniseg.StringWidth(Strip(text))
}

// Wrap word-wraps styled text to width. Each returned line is self-contained:
// it restores the SGR state active at its start and ends reset-free (callers
// append Reset when writing lines).
func Wrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	var lines []string

	for raw := range strings.SplitSeq(text, "\n") {
		lines = append(lines, wrapLine(raw, width)...)
	}

	return lines
}

func wrapLine(text string, width int) []string {
	p := &parser{}
	p.feed(text)

	if len(p.segments) == 0 {
		return []string{text}
	}

	var lines []string
	segments := p.segments

	start := 0
	curWidth := 0
	lastBreak := -1

	emit := func(from, to int) {
		var sb strings.Builder
		if from < len(segments) {
			if segments[from].state != "" {
				sb.WriteString(segments[from].state)
			}
		}
		prevState := ""
		if from < len(segments) {
			prevState = segments[from].state
		}
		for i := from; i < to; i++ {
			if segments[i].state != prevState {
				if segments[i].state == "" {
					sb.WriteString(Reset)
				} else {
					sb.WriteString(Reset)
					sb.WriteString(segments[i].state)
				}
				prevState = segments[i].state
			}
			sb.WriteString(segments[i].text)
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}

	for i := 0; i < len(segments); i++ {
		seg := segments[i]

		if seg.text == " " {
			lastBreak = i
		}

		if curWidth+seg.width > width && curWidth > 0 {
			if lastBreak >= start {
				emit(start, lastBreak)
				// Consume the whole space run at the break so a continuation
				// line neither starts with one nor wraps into a blank line.
				start = lastBreak + 1
				for start < len(segments) && segments[start].text == " " {
					start++
				}
			} else {
				emit(start, i)
				start = i
			}
			curWidth = 0
			for j := start; j <= i; j++ {
				curWidth += segments[j].width
			}
			lastBreak = -1
			if start > i {
				i = start - 1
			}
			continue
		}

		curWidth += seg.width
	}

	if start < len(segments) {
		emit(start, len(segments))
	}

	if len(lines) == 0 {
		lines = append(lines, "")
	}

	return lines
}

// Truncate cuts styled text to at most width columns, appending tail (plain)
// if anything was cut.
func Truncate(text string, width int, tail string) string {
	if Width(text) <= width {
		return text
	}

	tailWidth := Width(tail)
	budget := max(width-tailWidth, 0)

	p := &parser{}
	p.feed(text)

	var sb strings.Builder
	used := 0
	prevState := ""

	for _, seg := range p.segments {
		if used+seg.width > budget {
			break
		}
		if seg.state != prevState {
			if seg.state == "" {
				sb.WriteString(Reset)
			} else {
				sb.WriteString(Reset)
				sb.WriteString(seg.state)
			}
			prevState = seg.state
		}
		sb.WriteString(seg.text)
		used += seg.width
	}

	if prevState != "" {
		sb.WriteString(Reset)
	}
	sb.WriteString(tail)

	return sb.String()
}

// Pad extends styled text with spaces to exactly width columns (truncating if
// longer).
func Pad(text string, width int) string {
	w := Width(text)
	if w > width {
		return Truncate(text, width, "…")
	}
	return text + strings.Repeat(" ", width-w)
}

// Highlight applies style (an SGR prefix, e.g. Reverse) to the display
// columns [from, to) of styled text, preserving the text's own styling.
func Highlight(text string, from, to int, style string) string {
	if to <= from {
		return text
	}

	p := &parser{}
	p.feed(text)

	var sb strings.Builder
	col := 0
	prevState := ""
	inRegion := false

	setState := func(state string, region bool) {
		sb.WriteString(Reset)
		if state != "" {
			sb.WriteString(state)
		}
		if region {
			sb.WriteString(style)
		}
	}

	for _, seg := range p.segments {
		region := col >= from && col < to

		if region != inRegion || seg.state != prevState {
			setState(seg.state, region)
			inRegion = region
			prevState = seg.state
		}

		sb.WriteString(seg.text)
		col += seg.width
	}

	// Extend the highlight over trailing padding when the selection reaches
	// past the end of the line.
	if col < to && from <= col {
		if !inRegion {
			setState(prevState, true)
			inRegion = true
		}
		pad := to - col
		if pad > 0 {
			sb.WriteString(strings.Repeat(" ", pad))
		}
	}

	if inRegion {
		sb.WriteString(Reset)
	}

	return sb.String()
}

// CutPlain returns the plain-text content of styled text between display
// columns [from, to).
func CutPlain(text string, from, to int) string {
	p := &parser{}
	p.feed(text)

	var sb strings.Builder
	col := 0

	for _, seg := range p.segments {
		if col >= to {
			break
		}
		if col >= from {
			sb.WriteString(seg.text)
		}
		col += seg.width
	}

	return sb.String()
}
