package code

import (
	"context"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/changes"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

var diffPanelFixture = []changes.FileDiff{
	{
		Path:   "pkg/agent/client.go",
		Status: changes.StatusModified,
		Patch: strings.Join([]string{
			"diff --git a/pkg/agent/client.go b/pkg/agent/client.go",
			"index 1111111..2222222 100644",
			"--- a/pkg/agent/client.go",
			"+++ b/pkg/agent/client.go",
			"@@ -134,3 +134,6 @@ func complete() {",
			" \tif r.cacheKey != \"\" {",
			"+\tif m, ok := model.Find(r.model); ok && m.Verbosity != \"\" {",
			"+\t\tparams.Text.Verbosity = responses.ResponseTextConfigVerbosity(m.Verbosity)",
			"+\t}",
			" ",
			" \tif r.effort != \"\" {",
		}, "\n"),
	},
	{
		Path:   "notes.txt",
		Status: changes.StatusAdded,
		Patch: strings.Join([]string{
			"--- /dev/null",
			"+++ b/notes.txt",
			"@@ -0,0 +1 @@",
			"+" + strings.Repeat("long ", 30),
		}, "\n"),
	},
}

func newDiffPanelTestApp(t *testing.T, width, height int) (*App, *strings.Builder) {
	t.Helper()
	var out strings.Builder
	term := inline.NewTerminal(inline.WithIO(strings.NewReader(""), &out, func() (int, int) { return width, height }))
	term.Resized(width, height)
	term.EnterAlt()

	a := &App{ctx: context.Background(), agent: newUITestAgent(nil), sessionID: "session", editor: NewEditor()}
	a.WithTerminal(term)
	return a, &out
}

func TestDiffPanelLinesShowStatsAndFileLineNumbers(t *testing.T) {
	const width = 70
	header, lines, sections := renderDiffPanelLines(diffPanelFixture, nil, width)
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = ansi.Strip(line)
		if got := ansi.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, plain[i])
		}
	}
	text := ansi.Strip(header) + "\n" + strings.Join(plain, "\n")
	if len(sections) != 2 || sections[0].path != "pkg/agent/client.go" || plain[sections[0].nameRow] != "pkg/agent/client.go" {
		t.Fatalf("sections = %+v", sections)
	}

	for _, want := range []string{
		"2 files changed +4 -0",
		"pkg/agent/client.go",
		"+3 -0",
		"notes.txt",
		" 134  ",
		" 135 +",
		" 137 +",
		"   1 +long",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("panel missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"diff --git", "index 1111111", "+++ b/", "@@"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("panel leaks patch header %q", unwanted)
		}
	}

	// A wrapped addition repeats its marker on the continuation row.
	for i, line := range plain {
		if strings.HasPrefix(line, "   1 +long") {
			if i+1 >= len(plain) || !strings.HasPrefix(plain[i+1], "     +") {
				t.Fatalf("wrapped addition continuation = %q", plain[i+1])
			}
			return
		}
	}
	t.Fatal("wrapped addition not rendered")
}

func TestRenderSplitsWideTerminalForDiffPanel(t *testing.T) {
	a, out := newDiffPanelTestApp(t, 180, 20)
	a.diffPanel.diffs = diffPanelFixture
	a.chat = cellUser("hello", a.width())

	panel := a.diffPanelWidth(180)
	if panel < diffPanelMinWidth || panel > diffPanelMaxWidth {
		t.Fatalf("panel width = %d", panel)
	}
	if got := a.width(); got != 180-panel-2 {
		t.Fatalf("chat width = %d, want %d", got, 180-panel-2)
	}

	a.render()
	if !a.diffPanelShowing {
		t.Fatal("render did not mark the panel as showing")
	}
	text := ansi.Strip(out.String())
	if !strings.Contains(text, "2 files changed") || !strings.Contains(text, "hide diff") {
		t.Fatalf("frame lacks the panel or its footer hint:\n%s", text)
	}

	// Hiding the panel hands the full width back to the chat.
	if !a.toggleDiffPanel() || !a.diffPanel.hidden {
		t.Fatal("toggle on a wide terminal did not hide the panel")
	}
	if got := a.width(); got != 180 {
		t.Fatalf("chat width after hiding = %d, want 180", got)
	}
	a.render()
	if a.diffPanelShowing {
		t.Fatal("panel still marked showing after hiding")
	}
}

func TestDiffPanelStaysOffNarrowTerminals(t *testing.T) {
	a, out := newDiffPanelTestApp(t, 100, 20)
	a.diffPanel.diffs = diffPanelFixture

	if got := a.width(); got != 100 {
		t.Fatalf("narrow chat width = %d, want 100", got)
	}
	if a.toggleDiffPanel() {
		t.Fatal("narrow terminal toggled the panel instead of falling back to the overlay")
	}
	a.render()
	if strings.Contains(ansi.Strip(out.String()), "files changed") {
		t.Fatal("narrow frame rendered the diff panel")
	}
}

func TestDiffPanelWheelScrollsPanelOnly(t *testing.T) {
	a, _ := newDiffPanelTestApp(t, 180, 10)
	a.diffPanel.diffs = diffPanelFixture
	a.render()

	a.handleMouse(inline.MouseEvent{Kind: inline.MouseWheel, WheelDelta: 1, X: 179, Y: 5})
	if a.diffPanel.offset != 3 {
		t.Fatalf("panel offset = %d, want 3", a.diffPanel.offset)
	}
	if a.chatScroll != a.lastMaxScroll {
		t.Fatal("panel wheel moved the chat")
	}
}

func TestDiffPanelPinsSummaryAndCurrentFile(t *testing.T) {
	p := &diffPanel{diffs: diffPanelFixture}

	top := p.render(70, 8)
	if len(top) != 8 || !strings.Contains(ansi.Strip(top[0]), "2 files changed") {
		t.Fatalf("top frame = %q", ansi.Strip(strings.Join(top, "\n")))
	}
	if strings.Contains(ansi.Strip(top[1]), "pkg/agent/client.go") && ansi.Strip(top[2]) == strings.Repeat("─", 70) {
		t.Fatal("file name pinned before its own row scrolled away")
	}

	p.offset = p.sections[0].nameRow + 3
	scrolled := p.render(70, 8)
	if len(scrolled) != 8 {
		t.Fatalf("scrolled frame rows = %d", len(scrolled))
	}
	if !strings.Contains(ansi.Strip(scrolled[0]), "2 files changed") {
		t.Fatalf("summary not pinned: %q", ansi.Strip(scrolled[0]))
	}
	if got := strings.TrimSpace(ansi.Strip(scrolled[1])); got != "pkg/agent/client.go" {
		t.Fatalf("pinned file = %q", got)
	}
	if !strings.Contains(ansi.Strip(scrolled[3]), " 135 +") {
		t.Fatalf("content under pinned header = %q", ansi.Strip(scrolled[3]))
	}
	for i, line := range scrolled {
		if got := ansi.Width(line); got != 70 {
			t.Fatalf("row %d width = %d", i, got)
		}
	}

	p.offset = p.sections[1].nameRow + 1
	if got := strings.TrimSpace(ansi.Strip(p.render(70, 4)[1])); got != "notes.txt" {
		t.Fatalf("pinned file after second section = %q", got)
	}
}

func TestDiffPanelCapsVeryLongLines(t *testing.T) {
	patch := "@@ -0,0 +1 @@\n+" + strings.Repeat("x", 20_000)
	lines := renderDiffPatchLines(patch, 80)
	if len(lines) > 20 {
		t.Fatalf("a 20000 char line produced %d rows", len(lines))
	}
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "19000 more chars") {
		t.Fatalf("missing truncation marker:\n%s", ansi.Strip(strings.Join(lines, "\n")))
	}
}
