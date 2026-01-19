package plan

import (
	"fmt"
	"strings"
	"sync"
)

// Status represents the state of a plan step
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusSkipped    Status = "skipped"
)

// Step represents a single step in a plan
type Step struct {
	Description string `json:"description"`
	Status      Status `json:"status"`
}

// Plan represents a task execution plan with tracked steps
type Plan struct {
	Title string `json:"title,omitempty"`
	Steps []Step `json:"steps"`
}

// Manager holds the current plan state
type Manager struct {
	mu      sync.RWMutex
	current *Plan
}

// Global manager instance
var defaultManager = &Manager{}

// Reset clears the current plan (call at turn start)
func Reset() {
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()
	defaultManager.current = nil
}

// Set replaces the current plan with a new one
func Set(p *Plan) {
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()
	defaultManager.current = p
}

// Get returns the current plan (may be nil)
func Get() *Plan {
	defaultManager.mu.RLock()
	defer defaultManager.mu.RUnlock()
	return defaultManager.current
}

// UpdateStep updates the status of a step by index (0-based)
func UpdateStep(index int, status Status) error {
	defaultManager.mu.Lock()
	defer defaultManager.mu.Unlock()

	if defaultManager.current == nil {
		return fmt.Errorf("no plan exists")
	}

	if index < 0 || index >= len(defaultManager.current.Steps) {
		return fmt.Errorf("step index %d out of range (0-%d)", index, len(defaultManager.current.Steps)-1)
	}

	defaultManager.current.Steps[index].Status = status
	return nil
}

// IsComplete returns true if all steps are done or skipped
func IsComplete() bool {
	defaultManager.mu.RLock()
	defer defaultManager.mu.RUnlock()

	if defaultManager.current == nil || len(defaultManager.current.Steps) == 0 {
		return true
	}

	for _, step := range defaultManager.current.Steps {
		if step.Status != StatusDone && step.Status != StatusSkipped {
			return false
		}
	}
	return true
}

// HasPlan returns true if a plan exists
func HasPlan() bool {
	defaultManager.mu.RLock()
	defer defaultManager.mu.RUnlock()
	return defaultManager.current != nil
}

// Format returns a string representation of the current plan
func Format() string {
	defaultManager.mu.RLock()
	defer defaultManager.mu.RUnlock()

	if defaultManager.current == nil {
		return "No plan set."
	}

	var sb strings.Builder

	if defaultManager.current.Title != "" {
		fmt.Fprintf(&sb, "## Plan: %s\n\n", defaultManager.current.Title)
	}

	for i, step := range defaultManager.current.Steps {
		marker := " "

		switch step.Status {
		case StatusDone:
			marker = "✓"

		case StatusInProgress:
			marker = "→"

		case StatusSkipped:
			marker = "○"
		}

		fmt.Fprintf(&sb, "%d. [%s] %s\n", i+1, marker, step.Description)
	}

	return sb.String()
}
