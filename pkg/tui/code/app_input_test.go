package code

import (
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestCtrlJInsertsNewline(t *testing.T) {
	a := &App{editor: NewEditor()}
	a.editor.SetText("first line")

	a.handleKey(inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'j'})

	if got := a.editor.Text(); got != "first line\n" {
		t.Fatalf("editor text = %q, want newline without submission", got)
	}
}
