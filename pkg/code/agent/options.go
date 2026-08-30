package agent

import (
	"os"
	"strings"
)

// Options controls the built-in tools exposed by an Agent. Environment
// variables are resolved once when the agent starts and are combined with the
// explicitly supplied options.
type Options struct {
	DisableShell     bool
	DisableWebSearch bool
	DisableWebFetch  bool
}

func resolveOptions(options []Options) Options {
	resolved := Options{
		DisableShell:     environmentEnabled("WINGMAN_DISABLE_SHELL"),
		DisableWebSearch: environmentEnabled("WINGMAN_DISABLE_WEBSEARCH"),
		DisableWebFetch:  environmentEnabled("WINGMAN_DISABLE_WEBFETCH"),
	}
	for _, option := range options {
		resolved.DisableShell = resolved.DisableShell || option.DisableShell
		resolved.DisableWebSearch = resolved.DisableWebSearch || option.DisableWebSearch
		resolved.DisableWebFetch = resolved.DisableWebFetch || option.DisableWebFetch
	}
	return resolved
}

func environmentEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
