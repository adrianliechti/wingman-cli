package server

import "time"

type ServerOptions struct {
	Port int
}

type ToolEntry struct {
	ID        int
	Timestamp time.Time
	Tool      string
	Arguments string
	Result    string
	Error     string
	Duration  time.Duration
	Status    string // "running", "done", "error"
}
