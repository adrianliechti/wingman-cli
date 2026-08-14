// Package model is the single source of truth for the models wingman serves:
// display name, capability class and token limits. Values are kept in sync
// with the wingman-vscode catalog (src/models.ts).
package model

import (
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
	Name        string
	Description string
	Class       Class

	ID string

	Aliases []string

	Input  int
	Output int

	Context int
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
	if m.Context > 0 {
		return m.Context
	}

	return m.Input + m.Output
}

func (m Model) ids() []string {
	return append([]string{m.ID}, m.Aliases...)
}

// Models lists every known model in preference order: the first entry of a
// family, filter or class is the one picked automatically.
var Models = []Model{
	// Anthropic

	{
		ID: "claude-sonnet-5",

		Name:  "Claude Sonnet 5",
		Class: ClassMedium,

		Context: 1000000,
		Output:  128000,
	},
	{
		ID: "claude-opus-5",

		Name:  "Claude Opus 5",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},

	{
		ID: "claude-opus-4-8",

		Name:  "Claude Opus 4.8",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},

	{
		ID: "claude-opus-4-7",

		Name:  "Claude Opus 4.7",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},

	{
		ID: "claude-sonnet-4-6",

		Name:  "Claude Sonnet 4.6",
		Class: ClassMedium,

		Context: 1000000,
		Output:  128000,
	},
	{
		ID: "claude-opus-4-6",

		Name:  "Claude Opus 4.6",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},
	{
		ID: "claude-haiku-4-6",

		Name:  "Claude Haiku 4.6",
		Class: ClassSmall,

		Context: 200000,
		Output:  64000,
	},

	{
		ID: "claude-sonnet-4-5",

		Name:  "Claude Sonnet 4.5",
		Class: ClassMedium,

		Context: 200000,
		Output:  64000,
	},
	{
		ID: "claude-opus-4-5",

		Name:  "Claude Opus 4.5",
		Class: ClassLarge,

		Context: 200000,
		Output:  64000,
	},
	{
		ID: "claude-haiku-4-5",

		Name:  "Claude Haiku 4.5",
		Class: ClassSmall,

		Context: 200000,
		Output:  64000,
	},

	{
		ID: "claude-fable-5",

		Name:  "Claude Fable 5",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},
	{
		ID: "claude-mythos-5",

		Name:  "Claude Mythos 5",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},

	// OpenAI

	{
		ID:      "gpt-5.6-sol",
		Aliases: []string{"gpt-5.6"},

		Name:  "GPT 5.6 Sol",
		Class: ClassLarge,

		Context: 1050000,
		Output:  128000,
	},
	{
		ID: "gpt-5.6-terra",

		Name:  "GPT 5.6 Terra",
		Class: ClassMedium,

		Context: 1050000,
		Output:  128000,
	},
	{
		ID: "gpt-5.6-luna",

		Name:  "GPT 5.6 Luna",
		Class: ClassSmall,

		Context: 1050000,
		Output:  128000,
	},

	{
		ID: "gpt-5.5",

		Name:  "GPT 5.5",
		Class: ClassMedium,

		Context: 1050000,
		Output:  128000,
	},

	{
		ID: "gpt-5.4",

		Name:  "GPT 5.4",
		Class: ClassMedium,

		Context: 1050000,
		Output:  128000,
	},
	{
		ID: "gpt-5.4-mini",

		Name:  "GPT 5.4 Mini",
		Class: ClassSmall,

		Context: 400000,
		Output:  128000,
	},

	{
		ID: "gpt-5.3-codex",

		Name:  "GPT 5.3 Codex",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5.3-codex-spark",

		Name:  "GPT 5.3 Codex Spark",
		Class: ClassSmall,

		Context: 128000,
		Output:  32000,
	},

	{
		ID: "gpt-5.2-codex",

		Name:  "GPT 5.2 Codex",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5.2",

		Name:  "GPT 5.2",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},

	{
		ID: "gpt-5.1-codex-max",

		Name:  "GPT 5.1 Codex Max",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5.1-codex",

		Name:  "GPT 5.1 Codex",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5.1-codex-mini",

		Name:  "GPT 5.1 Codex Mini",
		Class: ClassSmall,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5.1",

		Name:  "GPT 5.1",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},

	{
		ID: "gpt-5-codex",

		Name:  "GPT 5 Codex",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5",

		Name:  "GPT 5",
		Class: ClassMedium,

		Context: 400000,
		Output:  128000,
	},
	{
		ID: "gpt-5-mini",

		Name:  "GPT 5 Mini",
		Class: ClassSmall,

		Context: 400000,
		Output:  128000,
	},

	// Google

	{
		ID: "gemini-3.6-flash",

		Name:  "Gemini 3.6 Flash",
		Class: ClassMedium,

		Input:  1048576,
		Output: 65536,
	},

	{
		ID:      "gemini-3.5-flash",
		Aliases: []string{"gemini-3-flash-preview"},

		Name:  "Gemini 3.5 Flash",
		Class: ClassMedium,

		Input:  1048576,
		Output: 65536,
	},
	{
		ID: "gemini-3.5-flash-lite",

		Name:  "Gemini 3.5 Flash Lite",
		Class: ClassSmall,

		Input:  1048576,
		Output: 65536,
	},

	{
		ID:      "gemini-3.1-pro",
		Aliases: []string{"gemini-3.1-pro-preview"},

		Name:  "Gemini 3.1 Pro",
		Class: ClassLarge,

		Input:  1048576,
		Output: 65536,
	},
	{
		ID: "gemini-3.1-flash-lite",

		Name:  "Gemini 3.1 Flash Lite",
		Class: ClassSmall,

		Input:  1048576,
		Output: 65536,
	},

	// Z.ai

	{
		ID: "glm-5.3",

		Name:  "GLM 5.3",
		Class: ClassMedium,

		Context: 1000000,
		Output:  131072,
	},

	{
		ID: "glm-5.2",

		Name:  "GLM 5.2",
		Class: ClassMedium,

		Context: 1000000,
		Output:  131072,
	},

	{
		ID: "glm-5.1",

		Name:  "GLM 5.1",
		Class: ClassMedium,

		Context: 200000,
		Output:  131072,
	},

	{
		ID: "glm-5",

		Name:  "GLM 5",
		Class: ClassMedium,

		Context: 204800,
		Output:  131072,
	},

	{
		ID: "glm-4.7",

		Name:  "GLM 4.7",
		Class: ClassMedium,

		Context: 204800,
		Output:  131072,
	},
	{
		ID: "glm-4.7-flash",

		Name:  "GLM 4.7 Flash",
		Class: ClassSmall,

		Context: 200000,
		Output:  131072,
	},

	// DeepSeek

	{
		ID: "deepseek-v4-pro",

		Name:  "DeepSeek V4 Pro",
		Class: ClassLarge,

		Context: 1000000,
		Output:  384000,
	},
	{
		ID: "deepseek-v4-flash",

		Name:  "DeepSeek V4 Flash",
		Class: ClassSmall,

		Context: 1000000,
		Output:  384000,
	},

	// Mistral

	{
		ID:      "mistral-medium",
		Aliases: []string{"mistral-medium-latest", "mistral-medium-2604"},

		Name:  "Mistral Medium 3.5",
		Class: ClassMedium,

		Context: 262144,
		Output:  262144,
	},
	{
		ID:      "mistral-small",
		Aliases: []string{"mistral-small-latest", "mistral-small-2603"},

		Name:  "Mistral Small 4",
		Class: ClassSmall,

		Context: 256000,
		Output:  256000,
	},

	// Moonshot

	{
		ID: "kimi-k3",

		Name:        "Kimi K3",
		Description: "Kimi’s most capable model to date, with 2.8 trillion parameters, native visual understanding, and a 1M-token context window, designed for frontier intelligence scenarios such as software engineering, knowledge work, and deep reasoning.",
		Class:       ClassLarge,

		Context: 1048576,
		Output:  131072,
	},

	{
		ID: "kimi-k2.7-code",

		Name:        "Kimi K2.7 Code",
		Description: "Kimi’s dedicated coding model. It follows instructions more reliably in long contexts, completes coding tasks with higher success rates. Context 256k.",
		Class:       ClassMedium,

		Context: 262144,
		Output:  262144,
	},

	// MiniMax

	{
		ID:      "minimax-m3",
		Aliases: []string{"MiniMax-M3"},

		Name:  "MiniMax M3",
		Class: ClassLarge,

		Context: 1000000,
		Output:  128000,
	},

	// xAI

	{
		ID: "grok-4.6",

		Name:  "Grok 4.6",
		Class: ClassLarge,

		Context: 500000,
		Output:  500000,
	},

	// Alibaba

	{
		ID: "qwen3.7-max",

		Name:  "Qwen 3.7 Max",
		Class: ClassLarge,

		Context: 1000000,
		Output:  65536,
	},
	{
		ID:      "qwen3.7-plus",
		Aliases: []string{"qwen3.7"},

		Name:  "Qwen 3.7 Plus",
		Class: ClassMedium,

		Context: 1000000,
		Output:  65536,
	},

	{
		ID:      "qwen3.6-plus",
		Aliases: []string{"qwen3.6"},

		Name:  "Qwen 3.6",
		Class: ClassMedium,

		Context: 1000000,
		Output:  65536,
	},
	{
		ID: "qwen3.6-flash",

		Name:  "Qwen 3.6 Flash",
		Class: ClassSmall,

		Context: 1000000,
		Output:  65536,
	},

	{
		ID:      "qwen3.5-plus",
		Aliases: []string{"qwen3.5"},

		Name:  "Qwen 3.5",
		Class: ClassMedium,

		Context: 1000000,
		Output:  65536,
	},

	{
		ID:      "qwen3-coder-plus",
		Aliases: []string{"qwen3-coder-next", "qwen3-coder"},

		Name:  "Qwen 3 Coder",
		Class: ClassMedium,

		Context: 1048576,
		Output:  65536,
	},
	{
		ID: "qwen3-coder-flash",

		Name:  "Qwen 3 Coder Flash",
		Class: ClassSmall,

		Context: 1048576,
		Output:  65536,
	},
	{
		ID:      "qwen3-next",
		Aliases: []string{"qwen3"},

		Name:  "Qwen 3",
		Class: ClassMedium,

		Input:   126976,
		Output:  32768,
		Context: 131072,
	},
}

// Available returns the catalog entries a backend serves, in preference order
// and with each ID resolved to the alias the backend accepts. A nil map means
// availability is unknown and every model is returned.
func Available(available map[string]bool) []Model {
	if available == nil {
		return slices.Clone(Models)
	}

	out := make([]Model, 0, len(Models))

	for _, m := range Models {
		for _, id := range m.ids() {
			if !available[id] {
				continue
			}

			m.ID = id
			out = append(out, m)

			break
		}
	}

	return out
}

func Find(id string) (Model, bool) {
	for _, m := range Models {
		if slices.Contains(m.ids(), id) {
			m.ID = id
			return m, true
		}
	}

	return Model{}, false
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
