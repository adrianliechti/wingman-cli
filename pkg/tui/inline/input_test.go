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
