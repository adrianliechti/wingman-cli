package code

import (
	"errors"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/clipboard"
)

func TestNormalizePastedText(t *testing.T) {
	got := normalizePastedText("one\r\ntwo\rthree\x00\x07\tfour \x1b[31mred\x1b[0m")
	if want := "one\ntwo\nthree\tfour red"; got != want {
		t.Fatalf("normalizePastedText() = %q, want %q", got, want)
	}
}

func TestHandlePasteRoutesStandalonePopupQuery(t *testing.T) {
	a := &App{
		agent:  newUITestAgent(nil),
		editor: NewEditor(),
		popup:  newPopup(popupList, "Pick", []PopupItem{{ID: "1", Label: "hello world"}}, nil),
	}

	a.handlePaste("hello\r\n world\x00")

	if got := a.popup.query; got != "hello world" {
		t.Fatalf("popup query = %q, want %q", got, "hello world")
	}
	if got := a.editor.Text(); got != "" {
		t.Fatalf("editor text = %q, want empty", got)
	}
}

func TestHandlePasteSeparatesExistingPopupQuery(t *testing.T) {
	popup := newPopup(popupList, "Pick", []PopupItem{{ID: "1", Label: "hello world"}}, nil)
	popup.SetQuery("hello")
	a := &App{agent: newUITestAgent(nil), editor: NewEditor(), popup: popup}

	a.handlePaste("world")

	if got := popup.query; got != "hello world" {
		t.Fatalf("popup query = %q, want %q", got, "hello world")
	}
}

func TestHandlePasteRoutesFocusedOverlaySearch(t *testing.T) {
	overlay := newTwoPaneOverlay("Problems", "", 1, nil, nil, func(int) string { return "nee dle" })
	overlay.searching = true
	a := &App{agent: newUITestAgent(nil), editor: NewEditor(), overlay: overlay}

	a.handlePaste("nee\r\ndle")

	if got := overlay.query; got != "nee dle" {
		t.Fatalf("overlay query = %q, want %q", got, "nee dle")
	}
	if len(overlay.filtered) != 1 {
		t.Fatalf("filtered = %v, want one match", overlay.filtered)
	}
	if got := a.editor.Text(); got != "" {
		t.Fatalf("editor text = %q, want empty", got)
	}
}

func TestHandlePasteDoesNotLeakThroughOverlay(t *testing.T) {
	overlay := newTwoPaneOverlay("Problems", "", 0, nil, nil, nil)
	a := &App{agent: newUITestAgent(nil), editor: NewEditor(), overlay: overlay}

	a.handlePaste("hidden")

	if got := a.editor.Text(); got != "" {
		t.Fatalf("editor text = %q, want empty", got)
	}
}

func TestTranscriptSearchAcceptsPaste(t *testing.T) {
	overlay := &transcriptOverlay{
		searching: true,
		selected:  0,
		entries: []transcriptEntry{{
			kind: transcriptAssistant, raw: "hello world", selectable: true,
		}},
	}

	if !overlay.HandlePaste("hello\r\nworld") {
		t.Fatal("focused transcript search did not handle paste")
	}
	if got := overlay.query; got != "hello world" {
		t.Fatalf("query = %q, want %q", got, "hello world")
	}
	if len(overlay.matches) != 1 {
		t.Fatalf("matches = %v, want one match", overlay.matches)
	}
}

func TestAskPasteStaysText(t *testing.T) {
	a := &App{agent: newUITestAgent(nil), editor: NewEditor(), askActive: true}
	a.handlePaste("./answer.txt")
	if got := a.editor.Text(); got != "./answer.txt" {
		t.Fatalf("editor text = %q, want path as answer text", got)
	}
	if len(a.pendingFiles) != 0 {
		t.Fatalf("pending files = %v, want none", a.pendingFiles)
	}
}

func TestClipboardImageDoesNotAttachBehindPopup(t *testing.T) {
	image := "data:image/png;base64,aW1hZ2U="
	a := &App{
		agent:  newUITestAgent(nil),
		editor: NewEditor(),
		popup:  newPopup(popupList, "Pick", nil, nil),
	}
	a.applyClipboardContents([]clipboard.Content{{Image: &image}}, nil)
	if len(a.pendingContent) != 0 {
		t.Fatalf("pending content = %v, want none", a.pendingContent)
	}
}

func TestPasteFromClipboardUsesInjectedReader(t *testing.T) {
	a := &App{
		agent:  newUITestAgent(nil),
		editor: NewEditor(),
		queue:  make(chan func(), 1),
		quit:   make(chan struct{}),
		clipboardRead: func() ([]clipboard.Content, error) {
			return []clipboard.Content{{Text: "one\r\ntwo\x00"}}, nil
		},
	}

	a.pasteFromClipboard()
	select {
	case fn := <-a.queue:
		fn()
	case <-time.After(time.Second):
		t.Fatal("clipboard callback was not posted")
	}

	if got := a.editor.Text(); got != "one\ntwo" {
		t.Fatalf("editor text = %q, want normalized paste", got)
	}
}

func TestApplyClipboardContentsShowsErrorsAndEmptyState(t *testing.T) {
	a := &App{}
	a.applyClipboardContents(nil, errors.New("unavailable"))
	if a.toast == nil || a.toast.message != "Clipboard paste failed: unavailable" {
		t.Fatalf("error toast = %#v", a.toast)
	}

	a.applyClipboardContents(nil, nil)
	if a.toast == nil || a.toast.message != "Clipboard is empty" {
		t.Fatalf("empty toast = %#v", a.toast)
	}
}

func TestLastAssistantTextIncludesAllVisibleBlocks(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "older"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolCall: &agent.ToolCall{Name: "read"}}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{
			{Text: "first"},
			{Text: "secret", Hidden: true},
			{ToolCall: &agent.ToolCall{Name: "shell"}},
			{Text: "second"},
		}},
	}

	if got := lastAssistantText(messages); got != "firstsecond" {
		t.Fatalf("lastAssistantText() = %q", got)
	}
}

func TestLastAssistantTextSkipsToolOnlyMessage(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{ToolCall: &agent.ToolCall{Name: "read"}}}},
	}
	if got := lastAssistantText(messages); got != "answer" {
		t.Fatalf("lastAssistantText() = %q, want answer", got)
	}
}

func TestLastAssistantTextSkipsWhitespaceOnlyMessage(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer"}}},
		{Role: agent.RoleAssistant, Content: []agent.Content{{Text: " \n\t"}}},
	}
	if got := lastAssistantText(messages); got != "answer" {
		t.Fatalf("lastAssistantText() = %q, want answer", got)
	}
}

func TestCopyLastResponseUsesInjectedWriter(t *testing.T) {
	var written string
	a := &App{
		agent: newUITestAgent([]agent.Message{{
			Role:    agent.RoleAssistant,
			Content: []agent.Content{{Text: "first"}, {Text: "second"}},
		}}),
		clipboardWrite: func(text string) error {
			written = text
			return nil
		},
	}

	a.copyLastResponse()
	if written != "firstsecond" {
		t.Fatalf("copied text = %q", written)
	}
}

func TestCopyLastResponseShowsMissingResponse(t *testing.T) {
	a := &App{agent: newUITestAgent(nil)}
	a.copyLastResponse()
	if a.toast == nil || a.toast.message != "No assistant response to copy" {
		t.Fatalf("toast = %#v", a.toast)
	}
}

func TestCopyLastResponseShowsBackendError(t *testing.T) {
	a := &App{
		agent: newUITestAgent([]agent.Message{{
			Role: agent.RoleAssistant, Content: []agent.Content{{Text: "answer"}},
		}}),
		clipboardWrite: func(string) error { return errors.New("unavailable") },
	}
	a.copyLastResponse()
	if a.toast == nil || a.toast.message != "Clipboard copy failed: unavailable" {
		t.Fatalf("toast = %#v", a.toast)
	}
}
