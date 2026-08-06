package code

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type namedUITestAgent struct{ *uiTestAgent }

func (a *namedUITestAgent) Name() string { return "codex" }

func TestComposerMovesIdentityToBottomRail(t *testing.T) {
	a := &App{agent: &namedUITestAgent{newUITestAgent(nil)}, sessionID: "session", editor: NewEditor()}

	normal, _ := a.editor.Render(64, 5, a.composerChrome(64))
	normalPlain := ansi.Strip(strings.Join(normal, "\n"))
	for _, unwanted := range []string{"AGENT", "codex"} {
		if strings.Contains(normalPlain, unwanted) {
			t.Fatalf("normal composer still shows %s: %q", unwanted, normalPlain)
		}
	}
	bottom := ansi.Strip(normal[len(normal)-1])
	for _, want := range []string{"GPT 5.6 Sol", "medium"} {
		if !strings.Contains(bottom, want) {
			t.Errorf("bottom rail missing %q: %q", want, bottom)
		}
	}
	if !strings.HasSuffix(bottom, "medium") {
		t.Fatalf("model identity is not aligned to the right edge: %q", bottom)
	}

	a.currentMode = ModePlan
	planChrome := a.composerChrome(64)
	plan, _ := a.editor.Render(64, 5, planChrome)
	planTop := ansi.Strip(plan[0])
	planBottom := ansi.Strip(plan[len(plan)-1])
	if !strings.Contains(planTop, "PLAN") {
		t.Fatalf("PLAN is not on the upper rail: %q", planTop)
	}
	if strings.Contains(planBottom, "PLAN") {
		t.Fatalf("PLAN remained on the lower rail: %q", planBottom)
	}
	if !strings.Contains(planBottom, "GPT 5.6 Sol · medium") {
		t.Fatalf("plan identity is not on the lower-right rail: %q", planBottom)
	}
	if want := colored(theme.Default.Yellow, "GPT 5.6 Sol · medium"); planChrome.BottomRight != want {
		t.Fatalf("plan identity is not yellow: %q", planChrome.BottomRight)
	}
}

func TestWelcomeStaysMinimal(t *testing.T) {
	a := &App{agent: newUITestAgent(nil), sessionID: "new"}
	plain := ansi.Strip(strings.Join(a.welcomeLines(100), "\n"))
	if !strings.Contains(plain, "/workspace") {
		t.Fatalf("welcome missing workspace: %s", plain)
	}
	for _, unwanted := range []string{"ctrl+p", "sessions", "GPT 5.6 Sol", "medium"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("welcome contains redundant %q: %s", unwanted, plain)
		}
	}
}

func TestSurfacesFitResponsiveWidths(t *testing.T) {
	a := &App{
		ctx: context.Background(), agent: newUITestAgent(nil), sessionID: "session",
		editor:          NewEditor(),
		pendingContent:  []agent.Content{{File: &agent.File{Data: "image"}}},
		pendingFiles:    []string{"pkg/tui/code/view.go"},
		lastInputTokens: 35, contextWindow: 100,
	}
	a.editor.SetText("A composer draft that wraps cleanly on narrow terminal windows")

	check := func(surface string, width int, lines []string) {
		t.Helper()
		for row, line := range lines {
			if got := ansi.Width(line); got > width {
				t.Fatalf("%s at width %d row %d overflowed to %d: %q", surface, width, row, got, line)
			}
		}
	}

	for _, width := range []int{40, 80, 120} {
		check("welcome", width, a.welcomeLines(width))
		lines, _ := a.editor.Render(width, 6, a.composerChrome(width))
		check("composer", width, lines)
		check("footer", width, []string{a.footerLine(width)})

		a.showCommandCenter()
		a.popup.maxRows = 8
		check("command center", width, a.popup.Render(width))
		a.popup = nil
	}
}

func TestChatScrollIndicatorOnFullWidthRow(t *testing.T) {
	var out strings.Builder
	term := inline.NewTerminal(inline.WithIO(strings.NewReader(""), &out, func() (int, int) { return 40, 10 }))
	term.Resized(40, 10)
	term.EnterAlt()

	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), sessionID: "session", editor: NewEditor()}
	a.WithTerminal(term)
	a.chat = cellUser(strings.Repeat("full width band content ", 30), 40)
	for range 30 {
		a.chat = append(a.chat, "tail")
	}
	a.follow = false
	a.chatScroll = 0
	a.render()

	plain := ansi.Strip(out.String())
	if !strings.Contains(plain, "↓ ") || !strings.Contains(plain, "more") {
		t.Fatalf("scroll indicator missing from frame: %q", plain)
	}
}
