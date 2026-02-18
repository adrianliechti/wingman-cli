package opencode

type modelEntry struct {
	id string

	inputTokens  int
	outputTokens int
}

type modelGroup struct {
	name string

	models []modelEntry
}

var candidates = []modelGroup{
	// Claude models
	{
		name: "Wingman Claude Opus",

		models: []modelEntry{
			{id: "claude-opus-4-6", inputTokens: 200000, outputTokens: 128000},
			{id: "claude-opus-4-5", inputTokens: 200000, outputTokens: 64000},
		},
	},
	{
		name: "Wingman Claude Sonnet",

		models: []modelEntry{
			{id: "claude-sonnet-4-6", inputTokens: 200000, outputTokens: 64000},
			{id: "claude-sonnet-4-5", inputTokens: 200000, outputTokens: 64000},
		},
	},
	{
		name: "Wingman Claude Haiku",

		models: []modelEntry{
			{id: "claude-haiku-4-6", inputTokens: 200000, outputTokens: 64000},
			{id: "claude-haiku-4-5", inputTokens: 200000, outputTokens: 64000},
		},
	},

	// OpenAI models
	{
		name: "Wingman Codex",

		models: []modelEntry{
			{id: "gpt-5.3-codex", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5.2-codex", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5.1-codex-max", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5.1-codex", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5-codex", inputTokens: 400000, outputTokens: 128000},
		},
	},
	{
		name: "Wingman Codex Mini",

		models: []modelEntry{
			{id: "gpt-5.3-codex-spark", inputTokens: 128000, outputTokens: 32000},
			{id: "gpt-5.1-codex-mini", inputTokens: 400000, outputTokens: 128000},
		},
	},
	{
		name: "Wingman ChatGPT",

		models: []modelEntry{
			{id: "gpt-5.2", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5.1", inputTokens: 400000, outputTokens: 128000},
			{id: "gpt-5", inputTokens: 400000, outputTokens: 128000},
		},
	},
	{
		name: "Wingman ChatGPT Mini",

		models: []modelEntry{
			{id: "gpt-5-mini", inputTokens: 400000, outputTokens: 128000},
		},
	},

	// Gemini models
	{
		name: "Wingman Gemini Pro",

		models: []modelEntry{
			{id: "gemini-3-pro", inputTokens: 200000, outputTokens: 64000},
			{id: "gemini-3-pro-preview", inputTokens: 200000, outputTokens: 64000},
			{id: "gemini-2.5-pro", inputTokens: 200000, outputTokens: 64000},
		},
	},
	{
		name: "Wingman Gemini Flash",

		models: []modelEntry{
			{id: "gemini-3-flash", inputTokens: 200000, outputTokens: 64000},
			{id: "gemini-3-flash-preview", inputTokens: 200000, outputTokens: 64000},
			{id: "gemini-2.5-flash", inputTokens: 200000, outputTokens: 64000},
		},
	},

	// Devstral models
	{
		name: "Wingman Devstral",

		models: []modelEntry{
			{id: "devstral", inputTokens: 256000, outputTokens: 256000},
			{id: "devstral-medium", inputTokens: 256000, outputTokens: 256000},
			{id: "devstral-medium-latest", inputTokens: 256000, outputTokens: 256000},
		},
	},
	{
		name: "Wingman Devstral Small",

		models: []modelEntry{
			{id: "devstral-small", inputTokens: 256000, outputTokens: 256000},
			{id: "devstral-small-latest", inputTokens: 256000, outputTokens: 256000},
		},
	},

	// GLM models
	{
		name: "Wingman GLM",

		models: []modelEntry{
			{id: "glm-5", inputTokens: 200000, outputTokens: 128000},
			{id: "glm-4.7", inputTokens: 200000, outputTokens: 128000},
		},
	},
	{
		name: "Wingman GLM Flash",

		models: []modelEntry{
			{id: "glm-4.7-flash", inputTokens: 200000, outputTokens: 128000},
		},
	},

	// Qwen models
	{
		name: "Wingman Qwen Coder",

		models: []modelEntry{
			{id: "qwen3-coder-next", inputTokens: 256000, outputTokens: 64000},
			{id: "qwen3-coder", inputTokens: 256000, outputTokens: 64000},
		},
	},
	{
		name: "Wingman Qwen",

		models: []modelEntry{
			{id: "qwen3.5", inputTokens: 256000, outputTokens: 64000},
			{id: "qwen3-next", inputTokens: 128000, outputTokens: 32000},
			{id: "qwen3", inputTokens: 128000, outputTokens: 16000},
		},
	},
}
