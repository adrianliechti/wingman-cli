package proxy

import (
	"net/url"
	"time"
)

type UserInfo struct {
	Name  string
	Email string
}

type RequestEntry struct {
	ID        string
	Timestamp time.Time

	Method string
	URL    *url.URL

	Status   int
	Duration time.Duration

	Model string

	InputTokens  int
	CachedTokens int
	OutputTokens int

	RequestBody  []byte
	ResponseBody []byte

	Error string
}
