package code

import (
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
)

func TestTurnActivityUsesFooterNotComposer(t *testing.T) {
	a := &App{agent: newUITestAgent(nil), sessionID: "session", editor: NewEditor()}
	a.setPhase(PhaseThinking)

	chrome := a.composerChrome(64)
	if strings.Contains(ansi.Strip(chrome.TopLabel), "Thinking") {
		t.Fatalf("activity leaked into composer chrome: %q", chrome.TopLabel)
	}
	footer := ansi.Strip(a.footerLine(64))
	if !strings.Contains(footer, "Thinking") || !strings.Contains(footer, "esc interrupt") {
		t.Fatalf("footer missing turn activity: %q", footer)
	}
}

func TestFooterUsageStaysCompact(t *testing.T) {
	a := &App{
		agent: newUITestAgent(nil), sessionID: "session",
		inputTokens: 1200, outputTokens: 345, lastInputTokens: 25, contextWindow: 100,
		usageVisibleUntil: time.Now().Add(time.Minute),
	}
	footer := ansi.Strip(a.footerLine(120))
	for _, want := range []string{"↑", "↓", "75%"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %q: %q", want, footer)
		}
	}
	for _, unwanted := range []string{"ctx ", " left", "█", "░"} {
		if strings.Contains(footer, unwanted) {
			t.Errorf("footer contains dashboard decoration %q: %q", unwanted, footer)
		}
	}
}

func TestBackgroundStatusUsesIdleFooter(t *testing.T) {
	a := &App{
		agent: newUITestAgent(nil), sessionID: "session",
		backgroundStatus: "Installing gopls (1/2)",
	}
	footer := ansi.Strip(a.footerLine(80))
	if !strings.Contains(footer, "Installing gopls (1/2)") {
		t.Fatalf("footer missing background status: %q", footer)
	}

	a.setPhase(PhaseThinking)
	footer = ansi.Strip(a.footerLine(80))
	if !strings.Contains(footer, "Thinking") || strings.Contains(footer, "Installing gopls") {
		t.Fatalf("turn activity did not take priority: %q", footer)
	}
}

func TestBackgroundWarningExpires(t *testing.T) {
	a := &App{
		backgroundStatus: "Could not install gopls", backgroundWarning: true,
		backgroundExpiry: time.Now().Add(time.Second),
	}
	a.expireBackgroundStatus(time.Now().Add(2 * time.Second))
	if a.backgroundStatus != "" || a.backgroundWarning || !a.backgroundExpiry.IsZero() {
		t.Fatalf("expired warning = (%q, %t, %v)", a.backgroundStatus, a.backgroundWarning, a.backgroundExpiry)
	}
}

func TestFooterUsageAppearsOnlyWhenFreshOrLow(t *testing.T) {
	a := &App{
		agent: newUITestAgent(nil), sessionID: "session",
		inputTokens: 1200, outputTokens: 345, lastInputTokens: 25, contextWindow: 100,
	}

	quiet := ansi.Strip(a.footerLine(80))
	if strings.Contains(quiet, "↑") || strings.Contains(quiet, "75%") {
		t.Fatalf("stale healthy usage remained visible: %q", quiet)
	}

	a.lastInputTokens = 75
	warning := ansi.Strip(a.footerLine(80))
	if !strings.Contains(warning, "25%") || strings.Contains(warning, "↑") {
		t.Fatalf("low-context footer should show only the warning percentage: %q", warning)
	}

	a.revealUsage(time.Now())
	fresh := ansi.Strip(a.footerLine(80))
	if !strings.Contains(fresh, "↑") || !strings.Contains(fresh, "25%") {
		t.Fatalf("fresh usage summary is incomplete: %q", fresh)
	}

	a.setPhase(PhaseThinking)
	working := ansi.Strip(a.footerLine(80))
	if !strings.Contains(working, "Thinking") || strings.Contains(working, "↑") || strings.Contains(working, "25%") {
		t.Fatalf("active footer mixed stale usage with activity: %q", working)
	}
}
