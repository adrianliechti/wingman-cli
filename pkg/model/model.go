// Package model is the single source of truth for the models wingman serves:
// display name, capability class and token limits. Values are kept in sync
// with the wingman-vscode catalog (src/models.ts).
package model

import (
	"os"
	"slices"
	"strings"
)

// Class buckets models by capability for automatic per-role selection: large
// drives planning, medium drives coding, small drives utility calls (recaps,
// compaction summaries) and the "fast" model of external agents.
type Class int

const (
	ClassMedium Class = iota
	ClassLarge
	ClassSmall
)

type Model struct {
	ID string

	Aliases   []string
	Namespace string

	Name        string
	Description string

	Class Class

	Input  int
	Output int

	Context          int
	ContextThreshold int

	Effort  string
	Efforts []string

	Verbosity string
}

func (m Model) InputTokens() int {
	if m.Input > 0 {
		return m.Input
	}

	if m.Output < m.Context {
		return m.Context - m.Output
	}

	return m.Context
}

func (m Model) OutputTokens() int {
	return m.Output
}

func (m Model) ContextTokens() int {
	context := m.Context
	if context == 0 {
		context = m.Input + m.Output
	}

	if !fullContextWindowEnabled() && m.ContextThreshold > 0 && m.ContextThreshold < context {
		return m.ContextThreshold
	}

	return context
}

func fullContextWindowEnabled() bool {
	if mode := strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_CONTEXT_WINDOW_MODE"))); mode != "" {
		return mode == "full"
	}

	// Deprecated compatibility alias for WINGMAN_CONTEXT_WINDOW_MODE=full.
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WINGMAN_LARGE_CONTEXT"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (m Model) ids() []string {
	return append([]string{m.ID}, m.Aliases...)
}

var gptEfforts = []string{"none", "low", "medium", "high", "xhigh"}
var gpt56Efforts = []string{"none", "low", "medium", "high", "xhigh", "max"}
var gpt6AstraEfforts = []string{"low", "medium", "high", "xhigh", "max"}
var claudeAlwaysThinkingEfforts = []string{"low", "medium", "high", "xhigh", "max"}
var qwen38Efforts = []string{"none", "low", "medium", "xhigh"}

// Models lists every known model in preference order: the first entry of a
// family, filter or class is the one picked automatically.
var Models = []Model{
	// Anthropic

	{
		ID: "claude-sonnet-5",

		Namespace: "anthropic",

		Name: "Claude Sonnet 5",

		Class: ClassMedium,

		Output: 128000,

		Context: 1000000,
	},
	{
		ID: "claude-opus-5",

		Namespace: "anthropic",

		Name: "Claude Opus 5",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,
	},

	{
		ID: "claude-opus-4-8",

		Namespace: "anthropic",

		Name: "Claude Opus 4.8",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,
	},

	{
		ID: "claude-opus-4-7",

		Namespace: "anthropic",

		Name: "Claude Opus 4.7",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,
	},

	{
		ID: "claude-sonnet-4-6",

		Namespace: "anthropic",

		Name: "Claude Sonnet 4.6",

		Class: ClassMedium,

		Output: 128000,

		Context: 1000000,
	},
	{
		ID: "claude-opus-4-6",

		Namespace: "anthropic",

		Name: "Claude Opus 4.6",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,
	},
	{
		ID: "claude-haiku-4-6",

		Namespace: "anthropic",

		Name: "Claude Haiku 4.6",

		Class: ClassSmall,

		Output: 64000,

		Context: 200000,
	},

	{
		ID: "claude-sonnet-4-5",

		Namespace: "anthropic",

		Name: "Claude Sonnet 4.5",

		Class: ClassMedium,

		Output: 64000,

		Context: 200000,
	},
	{
		ID: "claude-opus-4-5",

		Namespace: "anthropic",

		Name: "Claude Opus 4.5",

		Class: ClassLarge,

		Output: 64000,

		Context: 200000,
	},
	{
		ID: "claude-haiku-4-5",

		Namespace: "anthropic",

		Name: "Claude Haiku 4.5",

		Class: ClassSmall,

		Output: 64000,

		Context: 200000,
	},

	{
		ID: "claude-fable-5-1",

		Namespace: "anthropic",

		Name: "Claude Fable 5.1",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,

		Efforts: claudeAlwaysThinkingEfforts,
	},
	{
		ID: "claude-fable-5",

		Namespace: "anthropic",

		Name: "Claude Fable 5",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,

		Efforts: claudeAlwaysThinkingEfforts,
	},
	{
		ID: "claude-mythos-5-1",

		Namespace: "anthropic",

		Name: "Claude Mythos 5.1",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,

		Efforts: claudeAlwaysThinkingEfforts,
	},
	{
		ID: "claude-mythos-5",

		Namespace: "anthropic",

		Name: "Claude Mythos 5",

		Class: ClassLarge,

		Output: 128000,

		Context: 1000000,

		Efforts: claudeAlwaysThinkingEfforts,
	},

	// OpenAI

	{
		ID: "gpt-6-astra",

		Namespace: "openai",

		Name: "GPT 6 Astra",

		Class: ClassLarge,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Effort:    "low",
		Efforts:   gpt6AstraEfforts,
		Verbosity: "low",
	},
	{
		ID: "gpt-5.6-sol",

		Aliases:   []string{"gpt-5.6"},
		Namespace: "openai",

		Name: "GPT 5.6 Sol",

		Class: ClassLarge,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Efforts: gpt56Efforts,
	},
	{
		ID: "gpt-5.6-terra",

		Namespace: "openai",

		Name: "GPT 5.6 Terra",

		Class: ClassMedium,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Efforts: gpt56Efforts,
	},
	{
		ID: "gpt-5.6-luna",

		Namespace: "openai",

		Name: "GPT 5.6 Luna",

		Class: ClassSmall,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Efforts: gpt56Efforts,
	},

	{
		ID: "gpt-5.5",

		Namespace: "openai",

		Name: "GPT 5.5",

		Class: ClassMedium,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Efforts: gptEfforts,
	},

	{
		ID: "gpt-5.4",

		Namespace: "openai",

		Name: "GPT 5.4",

		Class: ClassMedium,

		Output: 128000,

		Context:          1050000,
		ContextThreshold: 272_000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.4-mini",

		Namespace: "openai",

		Name: "GPT 5.4 Mini",

		Class: ClassSmall,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},

	{
		ID: "gpt-5.3-codex",

		Namespace: "openai",

		Name: "GPT 5.3 Codex",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.3-codex-spark",

		Namespace: "openai",

		Name: "GPT 5.3 Codex Spark",

		Class: ClassSmall,

		Output: 32000,

		Context: 128000,

		Efforts: gptEfforts,
	},

	{
		ID: "gpt-5.2-codex",

		Namespace: "openai",

		Name: "GPT 5.2 Codex",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.2",

		Namespace: "openai",

		Name: "GPT 5.2",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},

	{
		ID: "gpt-5.1-codex-max",

		Namespace: "openai",

		Name: "GPT 5.1 Codex Max",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.1-codex",

		Namespace: "openai",

		Name: "GPT 5.1 Codex",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.1-codex-mini",

		Namespace: "openai",

		Name: "GPT 5.1 Codex Mini",

		Class: ClassSmall,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5.1",

		Namespace: "openai",

		Name: "GPT 5.1",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},

	{
		ID: "gpt-5-codex",

		Namespace: "openai",

		Name: "GPT 5 Codex",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5",

		Namespace: "openai",

		Name: "GPT 5",

		Class: ClassMedium,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},
	{
		ID: "gpt-5-mini",

		Namespace: "openai",

		Name: "GPT 5 Mini",

		Class: ClassSmall,

		Output: 128000,

		Context: 400000,

		Efforts: gptEfforts,
	},

	// Google

	{
		ID: "gemini-3.7-flash",

		Namespace: "google",

		Name: "Gemini 3.7 Flash",

		Class: ClassMedium,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},

	{
		ID: "gemini-3.6-flash",

		Namespace: "google",

		Name: "Gemini 3.6 Flash",

		Class: ClassMedium,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},

	{
		ID: "gemini-3.5-flash",

		Aliases:   []string{"gemini-3-flash-preview"},
		Namespace: "google",

		Name: "Gemini 3.5 Flash",

		Class: ClassMedium,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},
	{
		ID: "gemini-3.5-flash-lite",

		Namespace: "google",

		Name: "Gemini 3.5 Flash Lite",

		Class: ClassSmall,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},

	{
		ID: "gemini-3.1-pro",

		Aliases:   []string{"gemini-3.1-pro-preview"},
		Namespace: "google",

		Name: "Gemini 3.1 Pro",

		Class: ClassLarge,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},
	{
		ID: "gemini-3.1-flash-lite",

		Namespace: "google",

		Name: "Gemini 3.1 Flash Lite",

		Class: ClassSmall,

		Input:  1048576,
		Output: 65536,

		ContextThreshold: 200_000,
	},

	// Z.ai

	{
		ID: "glm-5.3",

		Namespace: "z-ai",

		Name: "GLM 5.3",

		Class: ClassMedium,

		Output: 131072,

		Context: 1000000,
	},
	{
		ID: "glm-5.2",

		Namespace: "z-ai",

		Name: "GLM 5.2",

		Class: ClassMedium,

		Output: 131072,

		Context: 1000000,
	},

	// DeepSeek

	{
		ID: "deepseek-v4-pro",

		Namespace: "deepseek",

		Name: "DeepSeek V4 Pro",

		Class: ClassLarge,

		Output: 384000,

		Context: 1000000,
	},
	{
		ID: "deepseek-v4-flash-0731",

		Namespace: "deepseek",

		Name: "DeepSeek V4 Flash 0731",

		Class: ClassSmall,

		Output: 384000,

		Context: 1000000,
	},
	{
		ID: "deepseek-v4-flash",

		Namespace: "deepseek",

		Name: "DeepSeek V4 Flash",

		Class: ClassSmall,

		Output: 384000,

		Context: 1000000,
	},

	// Mistral

	{
		ID: "mistral-medium",

		Aliases:   []string{"mistral-medium-latest", "mistral-medium-2604"},
		Namespace: "mistralai",

		Name: "Mistral Medium 3.5",

		Class: ClassMedium,

		Output: 262144,

		Context: 262144,
	},
	{
		ID: "mistral-small",

		Aliases:   []string{"mistral-small-latest", "mistral-small-2603"},
		Namespace: "mistralai",

		Name: "Mistral Small 4",

		Class: ClassSmall,

		Output: 256000,

		Context: 256000,
	},

	// Moonshot

	{
		ID: "kimi-k3",

		Namespace: "moonshotai",

		Name: "Kimi K3",

		Class: ClassLarge,

		Output: 131072,

		Context: 1048576,
	},

	// MiniMax

	{
		ID: "minimax-m3",

		Aliases:   []string{"MiniMax-M3"},
		Namespace: "minimax",

		Name: "MiniMax M3",

		Class: ClassLarge,

		Output: 128000,

		Context:          1000000,
		ContextThreshold: 512_000,
	},

	// xAI

	{
		ID: "grok-4.6",

		Namespace: "x-ai",

		Name: "Grok 4.6",

		Class: ClassLarge,

		Output: 500000,

		Context:          500000,
		ContextThreshold: 200_000,
	},

	// Alibaba

	{
		ID: "qwen3.8-max",

		Aliases:   []string{"qwen3.8-2.4t-a95b"},
		Namespace: "qwen",

		Name: "Qwen 3.8 Max",

		Class: ClassLarge,

		Output: 131072,

		Context: 1000000,
	},
	{
		ID: "qwen3.8",

		Aliases:   []string{"qwen3.8-27b"},
		Namespace: "qwen",

		Name: "Qwen 3.8",

		Class: ClassMedium,

		Output: 65536,

		Context: 262144,

		Efforts: qwen38Efforts,
	},

	{
		ID: "qwen3.7-max",

		Namespace: "qwen",

		Name: "Qwen 3.7 Max",

		Class: ClassLarge,

		Output: 65536,

		Context: 1000000,
	},
	{
		ID: "qwen3.7-plus",

		Aliases:   []string{"qwen3.7"},
		Namespace: "qwen",

		Name: "Qwen 3.7 Plus",

		Class: ClassMedium,

		Output: 65536,

		Context: 1000000,
	},

	{
		ID: "qwen3.5-plus",

		Aliases:   []string{"qwen3.5"},
		Namespace: "qwen",

		Name: "Qwen 3.5",

		Class: ClassMedium,

		Output: 65536,

		Context: 1000000,
	},
}

// Available returns the catalog entries a backend serves, in preference order
// and with each ID resolved to the alias the backend accepts. A nil map means
// availability is unknown and every model is returned.
func Available(available map[string]bool) []Model {
	if available == nil {
		return slices.Clone(Models)
	}

	ids := make([]string, 0, len(available))
	for id, enabled := range available {
		if enabled {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	resolved := make(map[int]string)
	for _, id := range ids {
		index, _, ok := find(id)
		if ok {
			resolved[index] = id
		}
	}

	out := make([]Model, 0, len(resolved))
	for index, m := range Models {
		if id, ok := resolved[index]; ok {
			m.ID = id
			out = append(out, m)
		}
	}

	return out
}

func Find(id string) (Model, bool) {
	_, m, ok := find(id)
	if ok {
		m.ID = id
	}
	return m, ok
}

func find(id string) (int, Model, bool) {
	matchID := id
	namespace := ""
	if prefix, suffix, ok := strings.Cut(matchID, "/"); ok {
		namespace = strings.TrimPrefix(prefix, "~")
		matchID = suffix
	}

	matchIndex := -1
	matchLength := 0

	for index, m := range Models {
		if namespace != "" && !strings.EqualFold(m.Namespace, namespace) {
			continue
		}
		for _, candidate := range m.ids() {
			if modelIDPrefix(matchID, candidate) && len(candidate) > matchLength {
				matchIndex = index
				matchLength = len(candidate)
			}
		}
	}

	if matchIndex < 0 {
		return 0, Model{}, false
	}
	return matchIndex, Models[matchIndex], true
}

func modelIDPrefix(id, prefix string) bool {
	if len(id) < len(prefix) || !strings.EqualFold(id[:len(prefix)], prefix) {
		return false
	}
	if len(id) == len(prefix) {
		return true
	}

	return id[len(prefix)] == ':'
}

func Normalize(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), ".", "-")
}

func Name(id string) string {
	if m, ok := Find(id); ok {
		return m.Name
	}

	return id
}

// Family groups models by vendor line (claude, gpt, glm, …) so automatic
// selection stays within one family when possible: switching families
// mid-session drops encrypted reasoning state.
func Family(id string) string {
	id = strings.ToLower(id)

	if i := strings.IndexAny(id, "-."); i > 0 {
		return id[:i]
	}

	return id
}

// ClassOf reports the class of a model, falling back to naming markers for
// ids the catalog does not know (external agents report their own models).
func ClassOf(id string) Class {
	if m, ok := Find(id); ok {
		return m.Class
	}

	id = strings.ToLower(id)

	for _, marker := range []string{"haiku", "luna", "flash", "mini", "nano", "spark"} {
		if strings.Contains(id, marker) {
			return ClassSmall
		}
	}

	for _, marker := range []string{"opus", "-sol", "fable", "mythos", "-pro", "-max"} {
		if strings.Contains(id, marker) {
			return ClassLarge
		}
	}

	return ClassMedium
}
