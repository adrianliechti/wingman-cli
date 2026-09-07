package inline

import "testing"

func testInputReader() (*inputReader, chan Event) {
	events := make(chan Event, 16)
	done := make(chan struct{})
	return &inputReader{events: events, done: done}, events
}

func nextEvent(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	default:
		t.Fatal("expected input event")
		return nil
	}
}

func TestInputReaderCtrlV(t *testing.T) {
	in, events := testInputReader()
	in.buf = []byte{0x16}
	in.process()

	event, ok := nextEvent(t, events).(KeyEvent)
	if !ok || event.Key != KeyCtrl || event.Rune != 'v' {
		t.Fatalf("event = %#v, want Ctrl+V", event)
	}
}

func TestInputReaderPasteShortcuts(t *testing.T) {
	for name, sequence := range map[string]string{
		"legacy":           "\x16",
		"legacy ctrl alt":  "\x1b\x16",
		"CSI u":            "\x1b[118;5u",
		"CSI u ctrl alt":   "\x1b[118;7u",
		"CSI u ctrl shift": "\x1b[118;6u",
		"uppercase":        "\x1b[86;6u",
		"kitty press":      "\x1b[118;5:1u",
		"kitty alternate":  "\x1b[118:86;6u",
		"kitty base key":   "\x1b[1084::118;5u",
		"caps lock":        "\x1b[118;69u",
		"xterm":            "\x1b[27;5;118~",
		"xterm ctrl alt":   "\x1b[27;7;118~",
		"shift insert":     "\x1b[2;2~",
	} {
		t.Run(name, func(t *testing.T) {
			// Exercise every possible read boundary, including split CSI parameters.
			for split := 0; split <= len(sequence); split++ {
				in, events := testInputReader()
				in.buf = []byte(sequence[:split])
				in.process()
				in.buf = append(in.buf, sequence[split:]...)
				in.process()
				event, ok := nextEvent(t, events).(KeyEvent)
				if !ok || event.Key != KeyCtrl || event.Rune != 'v' {
					t.Fatalf("split %d: event = %#v, want paste shortcut", split, event)
				}
				if len(events) != 0 || len(in.buf) != 0 {
					t.Fatalf("split %d: duplicate event or unconsumed input", split)
				}
			}
		})
	}
}

func TestInputReaderDoesNotPasteForUnrelatedExtendedKeys(t *testing.T) {
	for _, sequence := range []string{
		"\x1b[118u", "\x1b[118;1u", "\x1b[118;3u", "\x1b[118;9u",
		"\x1b[119;5u", "\x1b[118;5:2u", "\x1b[118;5:3u",
		"\x1b[118;0u", "\x1b[118;999999999999999999999u", "\x1b[118;5:9u",
		"\x1b[27;1;118~", "\x1b[2~", "\x1b[118;7;64u",
	} {
		t.Run(sequence, func(t *testing.T) {
			in, events := testInputReader()
			in.buf = []byte(sequence)
			in.process()
			if len(events) != 0 {
				t.Fatalf("unexpected shortcut: %#v", <-events)
			}
		})
	}
}

func TestInputReaderPastedShortcutsNeverReadClipboardOrSubmit(t *testing.T) {
	in, events := testInputReader()
	in.buf = []byte("\x1b[200~first\r\n\x16\x1b[118;5u\x1b[27;5;118~\x1b[2;2~last\x1b[201~")
	in.process()
	event, ok := nextEvent(t, events).(PasteEvent)
	if !ok || event.Text != "first\nlast" || len(events) != 0 {
		t.Fatalf("events escaped paste boundary: %#v (%d extra)", event, len(events))
	}
}

func TestInputReaderFocusEvents(t *testing.T) {
	in, events := testInputReader()
	in.buf = []byte("\x1b[I\x1b[O")
	in.process()

	gained, ok := nextEvent(t, events).(FocusEvent)
	if !ok || !gained.Focused {
		t.Fatalf("event = %#v, want focus gained", gained)
	}

	lost, ok := nextEvent(t, events).(FocusEvent)
	if !ok || lost.Focused {
		t.Fatalf("event = %#v, want focus lost", lost)
	}
}

func TestInputReaderBracketedPasteNormalizesNewlines(t *testing.T) {
	for name, input := range map[string]string{
		"CRLF": "one\r\ntwo",
		"LF":   "one\ntwo",
		"CR":   "one\rtwo",
	} {
		t.Run(name, func(t *testing.T) {
			in, events := testInputReader()
			in.buf = []byte("\x1b[200~" + input + "\x1b[201~")
			in.process()

			event, ok := nextEvent(t, events).(PasteEvent)
			if !ok || event.Text != "one\ntwo" {
				t.Fatalf("event = %#v, want normalized multiline paste", event)
			}
		})
	}
}
