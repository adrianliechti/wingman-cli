package app

import (
	"fmt"
	"sync"
	"time"

	"github.com/rivo/tview"
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
}

// NewSpinner creates a new spinner component
func NewSpinner(app *tview.Application, view *tview.TextView) *Spinner {
	return &Spinner{
		view:     view,
		app:      app,
		stopChan: make(chan struct{}),
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
		s.render()
		return
	}

	s.active = true
	s.ticker = time.NewTicker(100 * time.Millisecond)
	s.stopChan = make(chan struct{})

	s.render()
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
	s.view.SetText("")
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
		s.view.SetText("")
		return
	}
	frame := spinnerFrames[s.frame]
	s.view.SetText(fmt.Sprintf("[%s]%s %s[-]", config.Color, frame, config.Message))
}
