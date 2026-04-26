package code

// Model is a curated entry in wingman's UI model picker. It carries both the
// upstream provider's ID and a friendly display name.
type Model struct {
	ID   string
	Name string
}

// AvailableModels is the curated allowlist of models that wingman exposes in
// its UI. Lives in this package (not pkg/agent) so the underlying agent
// runtime stays provider-agnostic — pkg/agent doesn't know or care which
// models are "blessed."
var AvailableModels = []Model{
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},

	{ID: "gpt-5.5", Name: "GPT 5.5"},
	{ID: "gpt-5.4", Name: "GPT 5.4"},

	{ID: "gpt-5.3-codex", Name: "GPT 5.3 Codex"},
	{ID: "gpt-5.2-codex", Name: "GPT 5.2 Codex"},

	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
	{ID: "claude-opus-4-5", Name: "Claude Opus 4.5"},
}

// ModelName returns the friendly display name for a model ID, falling back
// to the ID itself if the model isn't in the curated list.
func ModelName(id string) string {
	for _, m := range AvailableModels {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}
