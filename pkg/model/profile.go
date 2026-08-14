package model

import "strings"

// Profile captures per-model harness constraints looked up by id prefix.
// Window is the full hardware context window. WindowCompaction is the smaller
// budget compaction runs against by default where long context carries a
// price premium (0 = compact against Window). Efforts lists the supported
// reasoning efforts sorted low→high; empty means unrestricted.
//
// Verified 2026-07: current Claude models (Opus 4.6+, Sonnet 4.6+, Fable 5)
// take 1M input tokens at flat per-token rates — no long-context premium;
// Haiku and pre-4.6 models are 200k hardware (Sonnet 4.5's 1M beta bills 2x
// above 200k and needs a beta header, so it stays capped). GPT-5.4/5.5 have
// 1M-class windows but bill 2x input / 1.5x output for the whole session
// once input exceeds 272k; GPT-5.6 (sol/terra/luna) keeps the short/long
// pricing split with an unpublished threshold, so it inherits the 272k
// budget. Codex and earlier GPT-5.x are 400k total, flat.
// Gemini bills ~2x above 200k prompts.
type Profile struct {
	Window           int
	WindowCompaction int
	Efforts          []string
}

func (p Profile) ContextWindow(large bool) int {
	if !large && p.WindowCompaction > 0 && p.WindowCompaction < p.Window {
		return p.WindowCompaction
	}
	return p.Window
}

var gptEfforts = []string{"none", "low", "medium", "high", "xhigh"}

var profiles = map[string]Profile{
	"claude-haiku":      {Window: 200_000},
	"claude-opus-4-5":   {Window: 200_000},
	"claude-opus-4-1":   {Window: 200_000},
	"claude-opus-4-0":   {Window: 200_000},
	"claude-sonnet-4-5": {Window: 200_000},
	"claude-sonnet-4-0": {Window: 200_000},
	"claude-3":          {Window: 200_000},
	"claude-":           {Window: 1_000_000},

	"gpt-5-6": {Window: 1_000_000, WindowCompaction: 272_000, Efforts: gptEfforts},
	"gpt-5-5": {Window: 1_000_000, WindowCompaction: 272_000, Efforts: gptEfforts},
	"gpt-5-4": {Window: 1_000_000, WindowCompaction: 272_000, Efforts: gptEfforts},
	"gpt-5":   {Window: 400_000, Efforts: gptEfforts},
	"gpt-4-1": {Window: 1_000_000, Efforts: gptEfforts},
	"gpt-4o":  {Window: 128_000, Efforts: gptEfforts},
	"gpt":     {Efforts: gptEfforts},
	"o3":      {Window: 200_000},
	"o4":      {Window: 200_000},

	"gemini-": {Window: 1_000_000, WindowCompaction: 200_000},
}

func Normalize(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), ".", "-")
}

func ProfileFor(id string) Profile {
	id = Normalize(id)

	match := ""
	for prefix := range profiles {
		if strings.HasPrefix(id, prefix) && len(prefix) > len(match) {
			match = prefix
		}
	}

	return profiles[match]
}
