package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func TestEditorArrowsFollowWrappedRowsBeforeHistory(t *testing.T) {
	editor := NewEditor()
	editor.AddHistory("older prompt")
	editor.SetText("one two three four five six seven eight")
	_, end := editor.Render(24, 10, EditorChrome{})
	if end.Row < 2 {
		t.Fatal("fixture did not wrap")
	}
	if !editor.HandleKey(inline.KeyEvent{Key: inline.KeyUp}) {
		t.Fatal("Up entered history from a wrapped continuation")
	}
	_, up := editor.Render(24, 10, EditorChrome{})
	if up.Row != end.Row-1 {
		t.Fatalf("Up moved from row %d to %d", end.Row, up.Row)
	}
	if !editor.HandleKey(inline.KeyEvent{Key: inline.KeyDown}) {
		t.Fatal("Down failed to return to the last row")
	}
	_, down := editor.Render(24, 10, EditorChrome{})
	if down != end {
		t.Fatalf("Down returned to %+v, want %+v", down, end)
	}
	for editor.HandleKey(inline.KeyEvent{Key: inline.KeyUp}) {
	}
	editor.HistoryPrev()
	if editor.Text() != "older prompt" {
		t.Fatal("Up at the first row did not recall history")
	}
	editor.HistoryNext()
	if editor.Text() != "one two three four five six seven eight" {
		t.Fatal("Down did not restore the draft")
	}
}

func TestEditorHistoryEditingStartsANewDraft(t *testing.T) {
	editor := NewEditor()
	editor.AddHistory("first")
	editor.AddHistory("second")
	editor.SetText("draft")
	editor.HistoryPrev()
	editor.HistoryPrev()
	if editor.Text() != "first" {
		t.Fatal("history order was lost")
	}
	editor.HistoryNext()
	editor.Insert(" edited")
	if editor.HistoryNext() {
		t.Fatal("Down overwrote an edited history entry")
	}
	editor.HistoryPrev()
	editor.HistoryNext()
	if editor.Text() != "second edited" {
		t.Fatalf("edited draft lost: %q", editor.Text())
	}
	editor.SetText("")
	if editor.HistoryNext() {
		t.Fatal("clearing the editor left stale history navigation")
	}
}

func TestEditorVerticalMovementPreservesDisplayColumns(t *testing.T) {
	editor := NewEditor()
	editor.SetText("abcdefgh\n日本語\nabcdefgh")
	editor.cursor = len([]rune("abcdefgh\n日本語\nabcd"))
	editor.Render(40, 10, EditorChrome{})
	editor.HandleKey(inline.KeyEvent{Key: inline.KeyUp})
	if editor.cursor != len([]rune("abcdefgh\n日本")) {
		t.Fatalf("cursor split the display column: %d", editor.cursor)
	}
	editor.HandleKey(inline.KeyEvent{Key: inline.KeyDown})
	if editor.cursor != len([]rune("abcdefgh\n日本語\nabcd")) {
		t.Fatalf("display column was not preserved: %d", editor.cursor)
	}
}

func TestEditorWrappingUpdatesAfterEditsAndResize(t *testing.T) {
	editor := NewEditor()
	editor.SetText("initial")
	editor.Render(40, 5, EditorChrome{})
	editor.Insert(" changed")
	lines, _ := editor.Render(40, 5, EditorChrome{})
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "initial changed") {
		t.Fatal("insert left stale rows")
	}
	editor.ReplaceRange(0, 7, "updated")
	editor.HandleKey(inline.KeyEvent{Key: inline.KeyBackspace})
	lines, _ = editor.Render(40, 5, EditorChrome{})
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "update changed") {
		t.Fatal("replacement or deletion left stale rows")
	}
	lines, _ = editor.Render(10, 10, EditorChrome{})
	for _, line := range lines {
		if ansi.Width(line) > 10 {
			t.Fatalf("resize left wide rows: %q", line)
		}
	}
}

func TestEditorChromeStaysWithinBounds(t *testing.T) {
	theme.SetDark()
	editor := NewEditor()
	editor.SetText("one two three four five six seven eight nine ten")
	lines, cursor := editor.Render(40, 6, EditorChrome{
		TopLabel:    colored(theme.Default.Yellow, "PLAN"),
		BottomRight: dim("GPT 5.6 Sol · medium"),
		TopColor:    theme.Default.Yellow,
		Attachments: []string{" image 1  file.go"},
	})
	if len(lines) > 6 {
		t.Fatalf("editor rendered %d rows, max 6", len(lines))
	}
	for i, line := range lines {
		if ansi.Width(line) > 40 {
			t.Fatalf("row %d width = %d: %q", i, ansi.Width(line), line)
		}
	}
	if cursor.Row < 0 || cursor.Row >= len(lines) || cursor.Col < 0 || cursor.Col >= 40 {
		t.Fatalf("cursor outside editor: %+v in %d rows", cursor, len(lines))
	}
	if lines[len(lines)-3] != "" {
		t.Fatalf("attachments are not separated from input: %q", lines)
	}
	if strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "@ manage") {
		t.Fatalf("composer still shows the manage hint: %q", lines)
	}
}

func TestEditorTextUsesChatContentInset(t *testing.T) {
	editor := NewEditor()
	editor.SetText("draft text")
	lines, cursor := editor.Render(10, 5, EditorChrome{})
	if got := ansi.Strip(lines[1]); !strings.HasPrefix(got, "❯ draft") {
		t.Fatalf("editor text is not aligned with chat content: %q", got)
	}
	if got := ansi.Strip(lines[2]); !strings.HasPrefix(got, "  text") {
		t.Fatalf("wrapped editor text lost its continuation inset: %q", got)
	}
	if cursor.Col != 2+len("text") {
		t.Fatalf("cursor column = %d, want %d", cursor.Col, 2+len("text"))
	}
}
