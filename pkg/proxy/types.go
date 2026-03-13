package proxy

import "time"

type RequestEntry struct {
	ID        int
	Timestamp time.Time
	Method    string
	Path      string
	Status    int
	Duration  time.Duration

	Model     string
	Streaming bool

	InputTokens  int
	OutputTokens int

	RequestBody  []byte
	ResponseBody []byte

	Error string
}