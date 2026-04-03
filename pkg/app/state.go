package app

import "github.com/adrianliechti/wingman-agent/pkg/ui/theme"

// Modal represents the currently active modal type
type Modal string

const (
	ModalNone       Modal = ""
	ModalPicker     Modal = "picker"
	ModalFilePicker Modal = "file-picker"
	ModalDiff       Modal = "diff"
	ModalReview     Modal = "review"
	ModalConfirm    Modal = "confirm"
)

// AppPhase represents the current operational phase of the application
type AppPhase int

const (
	PhaseIdle AppPhase = iota
	PhasePreparing
	PhaseThinking
	PhaseStreaming
	PhaseToolRunning
)

// PhaseConfig holds display configuration for each phase
type PhaseConfig struct {
	Message  string
	Color    string
	Animated bool
}

// GetPhaseConfig returns the display configuration for a given phase
func GetPhaseConfig(phase AppPhase) PhaseConfig {
	t := theme.Default

	switch phase {
	case PhasePreparing:
		return PhaseConfig{
			Message:  "Preparing...",
			Color:    t.BrBlack.String(),
			Animated: true,
		}
	case PhaseThinking, PhaseStreaming:
		return PhaseConfig{
			Message:  "Thinking...",
			Color:    t.Cyan.String(),
			Animated: true,
		}
	case PhaseToolRunning:
		return PhaseConfig{
			Message:  "Running...",
			Color:    t.Yellow.String(),
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
