package code

import "strings"

type Model struct {
	ID   string
	Name string
}

// ModelClass buckets models by capability for automatic per-role selection:
// large drives planning, medium drives coding, small drives utility calls
// (recaps, compaction summaries).
type ModelClass int

const (
	ModelClassMedium ModelClass = iota
	ModelClassLarge
	ModelClassSmall
)

func ModelClassOf(id string) ModelClass {
	id = strings.ToLower(id)

	for _, marker := range []string{"haiku", "luna", "flash", "mini", "nano"} {
		if strings.Contains(id, marker) {
			return ModelClassSmall
		}
	}
	for _, marker := range []string{"opus", "-sol", "fable", "mythos", "-pro"} {
		if strings.Contains(id, marker) {
			return ModelClassLarge
		}
	}
	return ModelClassMedium
}

// ModelFamilyOf groups models by vendor line (claude, gpt, glm, …) so automatic
// selection stays within one family when possible: switching families
// mid-session drops encrypted reasoning state.
func ModelFamilyOf(id string) string {
	id = strings.ToLower(id)
	if i := strings.IndexAny(id, "-."); i > 0 {
		return id[:i]
	}
	return id
}

var AvailableModels = []Model{
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
	{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"},

	{ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol"},
	{ID: "gpt-5.6-terra", Name: "GPT 5.6 Terra"},
	{ID: "gpt-5.6-luna", Name: "GPT 5.6 Luna"},

	{ID: "gpt-5.5", Name: "GPT 5.5"},
	{ID: "gpt-5.4", Name: "GPT 5.4"},

	{ID: "gpt-5.3-codex", Name: "GPT 5.3 Codex"},
	{ID: "gpt-5.2-codex", Name: "GPT 5.2 Codex"},

	{ID: "claude-opus-5", Name: "Claude Opus 5"},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8"},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
	{ID: "claude-opus-4-5", Name: "Claude Opus 4.5"},

	{ID: "claude-fable-5", Name: "Claude Fable 5"},
	{ID: "claude-mythos-5", Name: "Claude Mythos 5"},

	{ID: "glm-5.2", Name: "GLM 5.2"},
	{ID: "glm-5.1", Name: "GLM 5.1"},

	{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro"},
	{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash"},
}

// ModelEffortBounds returns the reasoning-effort range a model supports;
// empty means unbounded on that side. Callers clamp out-of-range requests to
// the nearest bound instead of failing. Only known constraints are listed.
func ModelEffortBounds(id string) (lowest, highest string) {
	if ModelFamilyOf(id) == "gpt" {
		return "", "xhigh"
	}
	return "", ""
}

func ModelName(id string) string {
	for _, m := range AvailableModels {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}
