package app

import "github.com/adrianliechti/wingman-cli/pkg/theme"

// AppPhase represents the current operational phase of the application
type AppPhase int

const (
	PhaseIdle AppPhase = iota
	PhaseThinking
	PhaseStreaming
	PhaseToolRunning
	PhaseCompacting
)

// PhaseConfig holds display configuration for each phase
type PhaseConfig struct {
	Message  string
	Color    string
	Animated bool
}

// GetPhaseConfig returns the display configuration for a given phase
func GetPhaseConfig(phase AppPhase, toolName string) PhaseConfig {
	t := theme.Default

	switch phase {
	case PhaseThinking:
		return PhaseConfig{
			Message:  "Thinking...",
			Color:    t.Cyan.String(),
			Animated: true,
		}
	case PhaseToolRunning:
		msg := "Running tool..."
		if toolName != "" {
			msg = "Running " + toolName + "..."
		}
		return PhaseConfig{
			Message:  msg,
			Color:    t.Yellow.String(),
			Animated: true,
		}
	case PhaseCompacting:
		return PhaseConfig{
			Message:  "Compacting context...",
			Color:    t.Magenta.String(),
			Animated: true,
		}
	default:
		return PhaseConfig{
			Message:  "",
			Color:    t.BrBlack.String(),
			Animated: false,
		}
	}
}

// Mode represents the operational mode
type Mode int

const (
	ModeAgent Mode = iota
	ModePlan
)
