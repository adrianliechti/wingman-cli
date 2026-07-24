package inline

import (
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const escTimeout = 30 * time.Millisecond

type inputReader struct {
	chunks chan []byte
	events chan<- Event
	done   <-chan struct{}

	buf     []byte
	pasting bool
	paste   strings.Builder
	pasteCR bool
}

func startInput(r io.Reader, events chan<- Event, done <-chan struct{}) {
	in := &inputReader{
		chunks: make(chan []byte, 8),
		events: events,
		done:   done,
	}

	go func() {
		for {
			b := make([]byte, 4096)
			n, err := r.Read(b)
			if n > 0 {
				select {
				case in.chunks <- b[:n]:
				case <-done:
					return
				}
			}
			if err != nil {
				close(in.chunks)
				return
			}
		}
	}()

	go in.run(done)
}

func (in *inputReader) run(done <-chan struct{}) {
	for {
		var timeout <-chan time.Time

		// A buffer holding a bare ESC (or an incomplete sequence) is ambiguous:
		// wait briefly for the rest before treating it as an Esc keypress.
		if len(in.buf) > 0 && in.buf[0] == 0x1b && !in.complete() {
			timeout = time.After(escTimeout)
		}

		if len(in.buf) > 0 && timeout == nil {
			in.process()
			continue
		}

		select {
		case chunk, ok := <-in.chunks:
			if !ok {
				return
			}
			in.buf = append(in.buf, chunk...)
		case <-timeout:
			// The continuation may have arrived at the same instant the timer
			// fired; prefer it over misreading the sequence as a bare Esc.
			select {
			case chunk, ok := <-in.chunks:
				if !ok {
					return
				}
				in.buf = append(in.buf, chunk...)
			default:
				in.processBareEsc()
			}
		case <-done:
			return
		}
	}
}

// complete reports whether the leading escape sequence in the buffer is fully
// received.
func (in *inputReader) complete() bool {
	buf := in.buf
	if len(buf) < 2 {
		return false
	}

	switch buf[1] {
	case '[', 'O':
		for i := 2; i < len(buf); i++ {
			if buf[i] >= 0x40 && buf[i] <= 0x7e {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func (in *inputReader) emit(ev Event) {
	if in.pasting {
		if k, ok := ev.(KeyEvent); ok {
			switch {
			case k.Key == KeyRune && !k.Alt:
				in.paste.WriteRune(k.Rune)
			case k.Key == KeyEnter:
				in.paste.WriteByte('\n')
			case k.Key == KeyTab:
				in.paste.WriteByte('\t')
			}
			return
		}
		return
	}
	select {
	case in.events <- ev:
	case <-in.done:
	}
}

func (in *inputReader) processBareEsc() {
	in.buf = in.buf[1:]
	in.emit(KeyEvent{Key: KeyEsc})
	if len(in.buf) > 0 {
		in.process()
	}
}

func (in *inputReader) process() {
	for len(in.buf) > 0 {
		b := in.buf[0]

		if b == 0x1b {
			if !in.complete() {
				return
			}
			in.consumeEscape()
			continue
		}

		in.buf = in.buf[1:]

		// Pasted newlines vary by terminal and platform. Preserve CR, LF, and
		// CRLF as one logical newline while bracketed paste mode is active.
		if in.pasting {
			switch b {
			case '\r':
				in.paste.WriteByte('\n')
				in.pasteCR = true
				continue
			case '\n':
				if !in.pasteCR {
					in.paste.WriteByte('\n')
				}
				in.pasteCR = false
				continue
			default:
				in.pasteCR = false
			}
		}

		switch {
		case b == 0x0d:
			in.emit(KeyEvent{Key: KeyEnter})
		case b == 0x09:
			in.emit(KeyEvent{Key: KeyTab})
		case b == 0x7f || b == 0x08:
			in.emit(KeyEvent{Key: KeyBackspace})
		case b < 0x20:
			in.emit(KeyEvent{Key: KeyCtrl, Rune: rune('a' + b - 1)})
		case b < utf8.RuneSelf:
			in.emit(KeyEvent{Key: KeyRune, Rune: rune(b)})
		default:
			in.consumeRune(b)
		}
	}
}

func (in *inputReader) consumeRune(first byte) {
	full := append([]byte{first}, in.buf...)
	r, size := utf8.DecodeRune(full)

	if r == utf8.RuneError && size <= 1 {
		if !utf8.FullRune(full) && len(full) < utf8.UTFMax {
			in.buf = full
			select {
			case chunk, ok := <-in.chunks:
				if ok {
					in.buf = append(in.buf, chunk...)
					b := in.buf[0]
					in.buf = in.buf[1:]
					in.consumeRune(b)
					return
				}
			case <-time.After(escTimeout):
			}
			in.buf = in.buf[1:]
			return
		}
		return
	}

	in.buf = full[size:]
	in.emit(KeyEvent{Key: KeyRune, Rune: r})
}

func (in *inputReader) consumeEscape() {
	buf := in.buf

	if len(buf) >= 2 && buf[1] != '[' && buf[1] != 'O' {
		in.buf = buf[2:]
		b := buf[1]
		switch {
		case b == 0x0d:
			in.emit(KeyEvent{Key: KeyEnter, Alt: true})
		case b == 0x7f || b == 0x08:
			in.emit(KeyEvent{Key: KeyBackspace, Alt: true})
		case b < 0x20:
			in.emit(KeyEvent{Key: KeyCtrl, Rune: rune('a' + b - 1), Alt: true})
		default:
			in.emit(KeyEvent{Key: KeyRune, Rune: rune(b), Alt: true})
		}
		return
	}

	end := 2
	for end < len(buf) && !(buf[end] >= 0x40 && buf[end] <= 0x7e) {
		end++
	}
	if end >= len(buf) {
		return
	}

	seq := string(buf[2 : end+1])
	in.buf = buf[end+1:]

	if strings.HasPrefix(seq, "<") {
		in.consumeMouse(seq)
		return
	}
	switch seq {
	case "I":
		in.emit(FocusEvent{Focused: true})
	case "O":
		in.emit(FocusEvent{Focused: false})
	case "A":
		in.emit(KeyEvent{Key: KeyUp})
	case "B":
		in.emit(KeyEvent{Key: KeyDown})
	case "C":
		in.emit(KeyEvent{Key: KeyRight})
	case "D":
		in.emit(KeyEvent{Key: KeyLeft})
	case "H", "1~", "7~":
		in.emit(KeyEvent{Key: KeyHome})
	case "F", "4~", "8~":
		in.emit(KeyEvent{Key: KeyEnd})
	case "Z":
		in.emit(KeyEvent{Key: KeyBacktab})
	case "3~":
		in.emit(KeyEvent{Key: KeyDelete})
	case "5~":
		in.emit(KeyEvent{Key: KeyPgUp})
	case "6~":
		in.emit(KeyEvent{Key: KeyPgDn})
	case "1;3A":
		in.emit(KeyEvent{Key: KeyUp, Alt: true})
	case "1;3B":
		in.emit(KeyEvent{Key: KeyDown, Alt: true})
	case "1;3C":
		in.emit(KeyEvent{Key: KeyRight, Alt: true})
	case "1;3D":
		in.emit(KeyEvent{Key: KeyLeft, Alt: true})
	case "200~":
		in.pasting = true
		in.paste.Reset()
		in.pasteCR = false
	case "201~":
		if in.pasting {
			in.pasting = false
			in.emit(PasteEvent{Text: in.paste.String()})
			in.paste.Reset()
			in.pasteCR = false
		}
	}
}

// consumeMouse handles SGR mouse reports ("<b;x;yM" / "<b;x;ym"): wheel
// motion, plus left-button press/drag/release for selection.
func (in *inputReader) consumeMouse(seq string) {
	body := seq[1 : len(seq)-1]
	release := seq[len(seq)-1] == 'm'

	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		return
	}

	num := func(s string) int {
		n := 0
		for i := 0; i < len(s); i++ {
			if s[i] < '0' || s[i] > '9' {
				return -1
			}
			n = n*10 + int(s[i]-'0')
		}
		return n
	}

	button, x, y := num(parts[0]), num(parts[1]), num(parts[2])
	if button < 0 {
		return
	}

	switch {
	case button == 64:
		in.emit(MouseEvent{Kind: MouseWheel, WheelDelta: -1, X: x, Y: y})
	case button == 65:
		in.emit(MouseEvent{Kind: MouseWheel, WheelDelta: 1, X: x, Y: y})
	case button&3 == 0 && release:
		in.emit(MouseEvent{Kind: MouseRelease, X: x, Y: y})
	case button&3 == 0 && button&32 != 0:
		in.emit(MouseEvent{Kind: MouseDrag, X: x, Y: y})
	case button&3 == 0:
		in.emit(MouseEvent{Kind: MousePress, X: x, Y: y})
	}
}
