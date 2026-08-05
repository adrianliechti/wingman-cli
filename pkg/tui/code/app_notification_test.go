package code

import (
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func TestBellOnlyWhenTerminalIsUnfocused(t *testing.T) {
	var output strings.Builder
	a := &App{
		term:        inline.NewTerminal(inline.WithIO(strings.NewReader(""), &output, func() (int, int) { return 40, 4 })),
		termFocused: true,
	}

	a.bellIfUnfocused()
	if output.Len() != 0 {
		t.Fatalf("focused terminal output = %q, want no bell", output.String())
	}

	a.handleEvent(inline.FocusEvent{Focused: false})
	a.bellIfUnfocused()
	if got := output.String(); got != "\a" {
		t.Fatalf("unfocused terminal output = %q, want bell", got)
	}
}

func TestToastExpiresWithoutTouchingQuitHint(t *testing.T) {
	a := &App{}
	a.showToast("copied", theme.Default.Cyan)
	a.footerHint = "press ctrl+c again"
	a.expireToast(time.Now().Add(toastDuration + time.Second))
	if a.toast != nil {
		t.Fatal("toast did not expire")
	}
	if a.footerHint == "" {
		t.Fatal("toast expiry cleared sticky quit hint")
	}
}
