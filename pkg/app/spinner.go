package app

import (
	"fmt"
	"sync"
	"time"

	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner provides an animated status indicator
type Spinner struct {
	view     *tview.TextView
	app      *tview.Application
	ticker   *time.Ticker
	stopChan chan struct{}

	mu       sync.Mutex
	active   bool
	frame    int
	phase    AppPhase
	toolName string

	// Callback to restore hint when stopped
	onStop func()
}

// NewSpinner creates a new spinner component that renders to the given view
func NewSpinner(app *tview.Application, view *tview.TextView, onStop func()) *Spinner {
	return &Spinner{
		view:     view,
		app:      app,
		stopChan: make(chan struct{}),
		onStop:   onStop,
	}
}

// Start begins the spinner animation with the given phase
func (s *Spinner) Start(phase AppPhase, toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.phase = phase
	s.toolName = toolName
	s.frame = 0

	if s.active {
		// Already running, just update the display
		s.render()
		s.app.QueueUpdateDraw(func() {})
		return
	}

	s.active = true
	s.ticker = time.NewTicker(100 * time.Millisecond)
	s.stopChan = make(chan struct{})

	s.render()
	s.app.QueueUpdateDraw(func() {})
	go s.run()
}

// SetPhase updates the spinner phase without restarting
func (s *Spinner) SetPhase(phase AppPhase, toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.phase = phase
	s.toolName = toolName

	if s.active {
		s.render()
		s.app.QueueUpdateDraw(func() {})
	}
}

// Stop halts the spinner animation
func (s *Spinner) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active {
		return
	}

	s.active = false
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.stopChan)

	// Restore the hint text
	if s.onStop != nil {
		s.onStop()
	}
}

// IsActive returns whether the spinner is currently animating
func (s *Spinner) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *Spinner) run() {
	for {
		select {
		case <-s.stopChan:
			return
		case <-s.ticker.C:
			s.mu.Lock()
			if !s.active {
				s.mu.Unlock()
				return
			}
			s.frame = (s.frame + 1) % len(spinnerFrames)
			s.render()
			s.mu.Unlock()

			s.app.QueueUpdateDraw(func() {})
		}
	}
}

func (s *Spinner) render() {
	config := GetPhaseConfig(s.phase, s.toolName)

	if config.Message == "" {
		if s.onStop != nil {
			s.onStop()
		}
		return
	}

	t := theme.Default
	frame := spinnerFrames[s.frame]

	var color string
	switch s.phase {
	case PhaseThinking:
		color = t.Cyan.String()

	case PhaseToolRunning:
		color = t.Yellow.String()

	case PhaseCompacting:
		color = t.Magenta.String()

	default:
		color = t.BrBlack.String()
	}

	s.view.SetText(fmt.Sprintf("[%s]%s %s[-]", color, frame, config.Message))
}
