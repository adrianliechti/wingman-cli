package shell

import (
	"os"
	"runtime"
	"strings"
)

// Options controls the host capabilities exposed to model-generated commands.
type Options struct {
	// ScratchDir receives complete shell transcripts when inline capture is
	// exceeded. Empty disables transcript persistence while retaining head/tail.
	ScratchDir string

	// ExtraWritableRoots grants the optional workspace sandbox write access to
	// directories outside the workspace, such as project memory and scratch data.
	ExtraWritableRoots []string

	// Environment extends DefaultEnvironmentPolicy. Allow can explicitly keep
	// a default-denied key; Deny adds patterns; Replace sets final values.
	// The WINGMAN_ENV_ALLOW variable ("KEY1,PREFIX_*") also extends Allow, so
	// denied keys can be restored without embedding code changes.
	Environment *EnvironmentPolicy
}

// EnvironmentPolicy filters the environment inherited by model-generated
// commands. Allow takes precedence over Deny. Replace is applied last and can
// intentionally reintroduce an otherwise denied variable.
type EnvironmentPolicy struct {
	Allow   []string
	Deny    []string
	Replace map[string]string
}

var defaultDeniedEnvironment = []string{
	"ANTHROPIC_*",
	"OPENAI_API_KEY",
	"OPENAI_ADMIN_KEY",
	"AZURE_OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"WINGMAN_API_KEY",
	"WINGMAN_AUTH_TOKEN",
	"WINGMAN_TOKEN",
}

func DefaultEnvironmentPolicy() EnvironmentPolicy {
	return EnvironmentPolicy{Deny: append([]string(nil), defaultDeniedEnvironment...)}
}

func environmentForTools(opts *Options) []string {
	policy := DefaultEnvironmentPolicy()
	for pattern := range strings.SplitSeq(os.Getenv("WINGMAN_ENV_ALLOW"), ",") {
		if pattern = strings.TrimSpace(pattern); pattern != "" {
			policy.Allow = append(policy.Allow, pattern)
		}
	}
	if opts != nil && opts.Environment != nil {
		policy.Allow = append(policy.Allow, opts.Environment.Allow...)
		policy.Deny = append(policy.Deny, opts.Environment.Deny...)
		policy.Replace = opts.Environment.Replace
	}
	return policy.Filter(os.Environ())
}

// Filter applies the policy to KEY=value entries. Matching is
// case-insensitive on Windows and supports a trailing * prefix pattern.
func (p EnvironmentPolicy) Filter(environ []string) []string {
	out := make([]string, 0, len(environ)+len(p.Replace))
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if matchesEnvironmentKey(key, p.Deny) && !matchesEnvironmentKey(key, p.Allow) {
			continue
		}
		out = append(out, entry)
	}
	for key, value := range p.Replace {
		out = setEnvironment(out, key, value)
	}
	return out
}

func matchesEnvironmentKey(key string, patterns []string) bool {
	if runtime.GOOS == "windows" {
		key = strings.ToUpper(key)
	}
	for _, pattern := range patterns {
		if runtime.GOOS == "windows" {
			pattern = strings.ToUpper(pattern)
		}
		if prefix, wildcard := strings.CutSuffix(pattern, "*"); wildcard {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		} else if key == pattern {
			return true
		}
	}
	return false
}

func setEnvironment(environ []string, key, value string) []string {
	filtered := environ[:0]
	for _, entry := range environ {
		entryKey, _, ok := strings.Cut(entry, "=")
		if ok && environmentKeysEqual(entryKey, key) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, key+"="+value)
}

func environmentKeysEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
