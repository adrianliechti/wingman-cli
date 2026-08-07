package code

type AppPhase int

const (
	PhaseIdle AppPhase = iota
	PhasePreparing
	PhaseThinking
	PhaseStreaming
	PhaseToolRunning
)
