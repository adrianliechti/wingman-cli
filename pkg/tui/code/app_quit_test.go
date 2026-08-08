package code

import (
	"context"
	"testing"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestPostReturnsWhenContextIsDoneAndQueueIsFull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{ctx: ctx, queue: make(chan func(), 1), quit: make(chan struct{})}
	a.queue <- func() {}
	cancel()

	done := make(chan struct{})
	go func() {
		a.post(func() {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("post remained blocked after context cancellation")
	}
}

func quitTestApp() *App {
	return &App{
		editor: NewEditor(),
		quit:   make(chan struct{}),
	}
}

func ctrlC() inline.KeyEvent {
	return inline.KeyEvent{Key: inline.KeyCtrl, Rune: 'c'}
}

func hasStopped(a *App) bool {
	select {
	case <-a.quit:
		return true
	default:
		return false
	}
}

func TestCtrlCRequiresConfirmationWhenIdle(t *testing.T) {
	a := quitTestApp()

	a.handleKey(ctrlC())
	if hasStopped(a) {
		t.Fatal("first ctrl+c stopped the app")
	}
	if a.footerHint != "Press ctrl+c again to exit" {
		t.Fatalf("footer hint = %q", a.footerHint)
	}

	a.handleKey(ctrlC())
	if !hasStopped(a) {
		t.Fatal("second ctrl+c did not stop the app")
	}
}

func TestCtrlCClearsInputBeforeArmingQuit(t *testing.T) {
	a := quitTestApp()
	a.editor.SetText("draft")

	a.handleKey(ctrlC())

	if a.editor.Text() != "" {
		t.Fatalf("editor text = %q", a.editor.Text())
	}
	if !a.quitDeadline.IsZero() {
		t.Fatal("clearing input armed the quit gate")
	}
	if hasStopped(a) {
		t.Fatal("clearing input stopped the app")
	}
}

func TestOtherKeyDisarmsQuitGate(t *testing.T) {
	a := quitTestApp()

	a.handleKey(ctrlC())
	a.handleKey(inline.KeyEvent{Key: inline.KeyEsc})

	if a.footerHint != "" {
		t.Fatalf("footer hint survived another key: %q", a.footerHint)
	}
	if !a.quitDeadline.IsZero() {
		t.Fatal("quit deadline survived after the hint was cleared")
	}

	a.handleKey(ctrlC())
	if hasStopped(a) {
		t.Fatal("ctrl+c exited without a visible warning")
	}
	if a.footerHint == "" {
		t.Fatal("ctrl+c after disarm did not warn again")
	}
}

func TestCtrlCClearingInputDisarmsQuitGate(t *testing.T) {
	a := quitTestApp()

	a.handleKey(ctrlC())
	a.pendingFiles = []string{"file.txt"}

	a.handleKey(ctrlC())
	if hasStopped(a) {
		t.Fatal("ctrl+c that cleared input stopped the app")
	}
	if a.footerHint != "" || !a.quitDeadline.IsZero() {
		t.Fatalf("gate still armed after input clear: hint=%q", a.footerHint)
	}
}

func TestQuitGateExpiresAsOneState(t *testing.T) {
	a := quitTestApp()
	if a.gateQuit("warning") {
		t.Fatal("first quit attempt passed the gate")
	}

	a.expireQuitGate(a.quitDeadline)

	if !a.quitDeadline.IsZero() {
		t.Fatal("expired quit deadline was retained")
	}
	if a.footerHint != "" {
		t.Fatalf("expired footer hint = %q", a.footerHint)
	}
	if a.gateQuit("warning") {
		t.Fatal("first attempt after expiry passed the gate")
	}
}

func TestQuitGateDoesNotCarryIntoNextTurn(t *testing.T) {
	a := quitTestApp()
	a.phase.Store(int32(PhaseStreaming))
	a.gateQuit("warning")

	a.setPhase(PhaseIdle)

	if !a.quitDeadline.IsZero() || a.footerHint != "" {
		t.Fatalf("quit gate remained armed after the turn became idle: hint=%q", a.footerHint)
	}
}

func TestQuitCommandWarningConfirmedByCtrlC(t *testing.T) {
	a := quitTestApp()
	if a.gateQuit("1 background agent is still running — press ctrl+c to exit and stop it") {
		t.Fatal("first quit attempt passed the gate")
	}

	a.handleKey(ctrlC())
	if !hasStopped(a) {
		t.Fatal("ctrl+c within the window did not exit")
	}
}

func TestSecondCtrlCWhileStreamingExits(t *testing.T) {
	a := quitTestApp()
	a.phase.Store(int32(PhaseStreaming))
	a.gateQuit("warning")

	a.handleKey(ctrlC())
	if !hasStopped(a) {
		t.Fatal("armed ctrl+c during streaming did not exit")
	}
}
