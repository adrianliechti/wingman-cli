package inline

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/term"
)

type Pos struct {
	Row int
	Col int
}

// Terminal drives a full-screen application in the alternate buffer: raw
// mode, input events, and row-diffed frame rendering. All methods must be
// called from a single goroutine.
type Terminal struct {
	in     *os.File
	reader io.Reader
	out    io.Writer

	events chan Event
	done   chan struct{}

	// sizeMu guards width/height: the resize watcher goroutine compares them
	// against fresh queries while the app goroutine updates them.
	sizeMu sync.Mutex
	width  int
	height int

	alt      bool
	altFrame []string

	mouse bool

	oldState *term.State
	sizeFn   func() (int, int)
}

type Option func(*Terminal)

const (
	enableBracketedPaste  = "\x1b[?2004h"
	disableBracketedPaste = "\x1b[?2004l"
	enableFocusReporting  = "\x1b[?1004h"
	disableFocusReporting = "\x1b[?1004l"
	saveWindowTitle       = "\x1b[22;0t"
	restoreWindowTitle    = "\x1b[23;0t"
)

// WithIO replaces the terminal's reader/writer, disabling raw-mode handling —
// used by tests.
func WithIO(r io.Reader, w io.Writer, size func() (int, int)) Option {
	return func(t *Terminal) {
		t.in = nil
		t.reader = r
		t.out = w
		t.sizeFn = size
	}
}

func NewTerminal(opts ...Option) *Terminal {
	t := &Terminal{
		in:     os.Stdin,
		reader: os.Stdin,
		out:    os.Stdout,
		events: make(chan Event, 32),
		done:   make(chan struct{}),
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *Terminal) Events() <-chan Event {
	return t.events
}

func (t *Terminal) Size() (int, int) {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()
	return t.width, t.height
}

func (t *Terminal) Start() error {
	if t.in != nil {
		state, err := term.MakeRaw(int(t.in.Fd()))
		if err != nil {
			return err
		}
		t.oldState = state

		if err := setupConsole(); err != nil {
			term.Restore(int(t.in.Fd()), t.oldState)
			return err
		}
	}

	w, h := t.querySize()
	t.sizeMu.Lock()
	t.width, t.height = w, h
	t.sizeMu.Unlock()

	// Bracketed paste keeps pasted newlines from being interpreted as submits;
	// focus reporting lets the app notify only when it is in the background.
	fmt.Fprint(t.out, saveWindowTitle+enableBracketedPaste+enableFocusReporting)

	startInput(t.reader, t.events, t.done)
	t.watchResize()

	return nil
}

// EnableMouse turns SGR mouse reporting on or off; off restores the
// terminal's native text selection.
func (t *Terminal) EnableMouse(on bool) {
	if t.mouse == on {
		return
	}
	t.mouse = on
	if on {
		fmt.Fprint(t.out, "\x1b[?1000;1002;1006h")
	} else {
		fmt.Fprint(t.out, "\x1b[?1000;1002;1006l")
	}
}

func (t *Terminal) MouseEnabled() bool {
	return t.mouse
}

func (t *Terminal) Stop() {
	close(t.done)

	t.EnableMouse(false)

	if t.alt {
		t.ExitAlt()
	}

	fmt.Fprint(t.out, disableFocusReporting+disableBracketedPaste+restoreWindowTitle+"\x1b[0m\x1b[?25h")

	if t.in != nil && t.oldState != nil {
		term.Restore(int(t.in.Fd()), t.oldState)
	}
}

func (t *Terminal) querySize() (int, int) {
	if t.sizeFn != nil {
		return t.sizeFn()
	}
	// Windows can only report the size on the output handle
	// (GetConsoleScreenBufferInfo); stdin works everywhere else.
	if f, ok := t.out.(*os.File); ok {
		if w, h, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w, h
		}
	}
	if t.in != nil {
		if w, h, err := term.GetSize(int(t.in.Fd())); err == nil && w > 0 {
			return w, h
		}
	}
	return 80, 24
}

// checkResize re-queries the terminal size and posts a ResizeEvent when it
// changed; called from the platform resize watcher.
func (t *Terminal) checkResize() {
	w, h := t.querySize()

	t.sizeMu.Lock()
	changed := w != t.width || h != t.height
	t.sizeMu.Unlock()

	if changed {
		select {
		case t.events <- ResizeEvent{Width: w, Height: h}:
		case <-t.done:
		}
	}
}

// Resized must be called by the app when it receives a ResizeEvent, before
// re-rendering.
func (t *Terminal) Resized(w, h int) {
	t.sizeMu.Lock()
	t.width = w
	t.height = h
	t.sizeMu.Unlock()

	t.altFrame = nil
}

func (t *Terminal) EnterAlt() {
	if t.alt {
		return
	}
	t.alt = true
	t.altFrame = nil
	fmt.Fprint(t.out, "\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
}

func (t *Terminal) ExitAlt() {
	if !t.alt {
		return
	}
	t.alt = false
	fmt.Fprint(t.out, "\x1b[?1049l")
}

// SetTitle updates the terminal window title using OSC 2. Control characters
// are stripped so workspace names cannot terminate or inject a sequence.
func (t *Terminal) SetTitle(title string) {
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, title)
	fmt.Fprint(t.out, "\x1b]2;"+title+"\x1b\\")
}

// Bell requests the terminal's configured audible or visual notification.
func (t *Terminal) Bell() {
	fmt.Fprint(t.out, "\a")
}

// RenderAlt draws a full-screen frame, rewriting only rows that changed
// since the previous frame. cursor, when non-nil, positions and shows the
// hardware cursor (row is a frame index).
func (t *Terminal) RenderAlt(lines []string, cursor *Pos) {
	if !t.alt {
		return
	}

	var frame strings.Builder
	frame.WriteString("\x1b[?2026h\x1b[?25l")

	for i := 0; i < t.height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		if i < len(t.altFrame) && t.altFrame[i] == line {
			continue
		}
		fmt.Fprintf(&frame, "\x1b[%d;1H\x1b[2K%s\x1b[0m", i+1, line)
	}

	t.altFrame = append(t.altFrame[:0], lines...)

	if cursor != nil {
		fmt.Fprintf(&frame, "\x1b[%d;%dH\x1b[?25h", cursor.Row+1, cursor.Col+1)
	}

	frame.WriteString("\x1b[?2026l")
	io.WriteString(t.out, frame.String())
}
