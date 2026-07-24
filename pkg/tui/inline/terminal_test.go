package inline

import (
	"strings"
	"testing"
)

// vtScreen is a minimal terminal emulator covering the sequences the
// renderer emits: CUP, EL, ED.
type vtScreen struct {
	rows   []string
	row    int
	h      int
	writes int
}

func newVTScreen(h int) *vtScreen {
	return &vtScreen{rows: make([]string, h), h: h}
}

func (s *vtScreen) Write(p []byte) (int, error) {
	text := string(p)

	for len(text) > 0 {
		if text[0] == 0x1b {
			seq, rest := readEsc(text)
			s.apply(seq)
			text = rest
			continue
		}

		switch text[0] {
		case '\n', '\r':
			text = text[1:]
		default:
			idx := strings.IndexAny(text, "\x1b\n\r")
			if idx < 0 {
				idx = len(text)
			}
			s.rows[s.row] += text[:idx]
			s.writes++
			text = text[idx:]
		}
	}

	return len(p), nil
}

func (s *vtScreen) apply(seq string) {
	if !strings.HasPrefix(seq, "\x1b[") {
		return
	}
	body := seq[2 : len(seq)-1]

	switch seq[len(seq)-1] {
	case 'H':
		row := 1
		if body != "" && body[0] != '?' {
			parts := strings.SplitN(body, ";", 2)
			r := 0
			for i := 0; i < len(parts[0]); i++ {
				r = r*10 + int(parts[0][i]-'0')
			}
			if r > 0 {
				row = r
			}
		}
		if row > s.h {
			row = s.h
		}
		s.row = row - 1
	case 'K':
		s.rows[s.row] = ""
	case 'J':
		if body == "2" {
			for i := range s.rows {
				s.rows[i] = ""
			}
		}
	}
}

func readEsc(text string) (string, string) {
	for i := 2; i < len(text); i++ {
		b := text[i]
		if b >= 0x40 && b <= 0x7e {
			return text[:i+1], text[i+1:]
		}
	}
	return text, ""
}

func newTestTerminal(h int) (*Terminal, *vtScreen) {
	screen := newVTScreen(h)
	t := NewTerminal(WithIO(strings.NewReader(""), screen, func() (int, int) { return 40, h }))
	t.width, t.height = 40, h
	t.alt = true
	return t, screen
}

func TestRenderAltFillsFrame(t *testing.T) {
	term, screen := newTestTerminal(4)

	term.RenderAlt([]string{"one", "two"}, nil)

	if screen.rows[0] != "one" || screen.rows[1] != "two" || screen.rows[2] != "" || screen.rows[3] != "" {
		t.Fatalf("rows = %q", screen.rows)
	}
}

func TestRenderAltRewritesOnlyChangedRows(t *testing.T) {
	term, screen := newTestTerminal(4)

	term.RenderAlt([]string{"a", "b", "c"}, nil)
	screen.writes = 0

	term.RenderAlt([]string{"a", "B", "c"}, nil)

	if screen.writes != 1 {
		t.Fatalf("writes = %d, want 1 (only the changed row)", screen.writes)
	}
	if screen.rows[1] != "B" {
		t.Fatalf("rows = %q", screen.rows)
	}
}

func TestRenderAltClearsRemovedRows(t *testing.T) {
	term, screen := newTestTerminal(4)

	term.RenderAlt([]string{"a", "b", "c"}, nil)
	term.RenderAlt([]string{"a"}, nil)

	if screen.rows[0] != "a" || screen.rows[1] != "" || screen.rows[2] != "" {
		t.Fatalf("rows = %q", screen.rows)
	}
}

func TestResizedForcesFullRepaint(t *testing.T) {
	term, screen := newTestTerminal(4)

	term.RenderAlt([]string{"a", "b"}, nil)
	term.Resized(40, 4)
	screen.writes = 0

	term.RenderAlt([]string{"a", "b"}, nil)

	if screen.writes != 2 {
		t.Fatalf("writes = %d, want full repaint of content rows", screen.writes)
	}
}

func TestTerminalTitleAndBell(t *testing.T) {
	var output strings.Builder
	term := NewTerminal(WithIO(strings.NewReader(""), &output, func() (int, int) { return 40, 4 }))

	term.SetTitle("Wingman\a\x1b — project")
	term.Bell()

	if got, want := output.String(), "\x1b]2;Wingman — project\x1b\\\a"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
