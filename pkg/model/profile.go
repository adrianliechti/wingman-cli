package model

import "strings"

type ReasoningEffortPlacement int

const (
	ReasoningEffortInObject ReasoningEffortPlacement = iota
	ReasoningEffortAtRoot
)

// Profile captures per-model harness constraints looked up by id prefix.
// Window is the full hardware context window. WindowCompaction is the smaller
// budget compaction runs against by default where long context carries a
// price premium (0 = compact against Window). Efforts lists the supported
// reasoning efforts sorted low→high; empty means unrestricted. DefaultEffort
// overrides the harness role default. ReasoningEffortPlacement describes the
// provider's JSON request shape.
//
// Verified 2026-07: current Claude models (Opus 4.6+, Sonnet 4.6+, Fable 5)
// take 1M input tokens at flat per-token rates — no long-context premium;
// Haiku and pre-4.6 models are 200k hardware (Sonnet 4.5's 1M beta bills 2x
// above 200k and needs a beta header, so it stays capped). GPT-5.4/5.5 have
// 1M-class windows but bill 2x input / 1.5x output for the whole session
// once input exceeds 272k; GPT-5.6 (sol/terra/luna) keeps the same threshold.
// Codex and earlier GPT-5.x are 400k total, flat.
// Gemini and Grok bill more above 200k prompts. MiniMax M3's long-context
// pricing starts above 512k.
type Profile struct {
	Window                   int
	WindowCompaction         int
	Efforts                  []string
	DefaultEffort            string
	ReasoningEffortPlacement ReasoningEffortPlacement
}

func (p Profile) ContextWindow(large bool) int {
	if !large && p.WindowCompaction > 0 && p.WindowCompaction < p.Window {
		return p.WindowCompaction
	}
	return p.Window
}

var gptEfforts = []string{"none", "low", "medium", "high", "xhigh"}
var gpt56Efforts = []string{"none", "low", "medium", "high", "xhigh", "max"}
var kimiK3Efforts = []string{"low", "high", "max"}

var profiles = map[string]Profile{
	"claude-haiku":      {Window: 200_000},
	"claude-opus-4-5":   {Window: 200_000},
	"claude-opus-4-1":   {Window: 200_000},
	"claude-opus-4-0":   {Window: 200_000},
	"claude-sonnet-4-5": {Window: 200_000},
	"claude-sonnet-4-0": {Window: 200_000},
	"claude-3":          {Window: 200_000},
	"claude":            {Window: 1_000_000},

	"gpt-5-6":      {Window: 1_050_000, WindowCompaction: 272_000, Efforts: gpt56Efforts},
	"gpt-5-5":      {Window: 1_050_000, WindowCompaction: 272_000, Efforts: gptEfforts},
	"gpt-5-4-mini": {Window: 400_000, Efforts: gptEfforts},
	"gpt-5-4-nano": {Window: 400_000, Efforts: gptEfforts},
	"gpt-5-4":      {Window: 1_050_000, WindowCompaction: 272_000, Efforts: gptEfforts},
	"gpt-5":        {Window: 400_000, Efforts: gptEfforts},
	"gpt-4-1":      {Window: 1_000_000, Efforts: gptEfforts},
	"gpt-4o":       {Window: 128_000, Efforts: gptEfforts},
	"gpt":          {Efforts: gptEfforts},
	"o3":           {Window: 200_000},
	"o4":           {Window: 200_000},

	"gemini": {Window: 1_000_000, WindowCompaction: 200_000},

	"glm-5-3":       {Window: 1_000_000},
	"glm-5-2":       {Window: 1_000_000},
	"glm-5-1":       {Window: 200_000},
	"glm-5":         {Window: 204_800},
	"glm-4-7-flash": {Window: 200_000},
	"glm-4-7":       {Window: 204_800},

	"kimi-k3": {
		Window:                   1_048_576,
		Efforts:                  kimiK3Efforts,
		DefaultEffort:            "max",
		ReasoningEffortPlacement: ReasoningEffortAtRoot,
	},
	"kimi-k2-7": {Window: 262_144},

	"minimax-m3": {Window: 1_000_000, WindowCompaction: 512_000},
	"grok-4-6":   {Window: 500_000, WindowCompaction: 200_000},
}

func Normalize(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), ".", "-")
}

func ProfileFor(id string) Profile {
	id = Normalize(id)

	match := ""
	for prefix := range profiles {
		if (id == prefix || strings.HasPrefix(id, prefix+"-")) && len(prefix) > len(match) {
			match = prefix
		}
	}

	return profiles[match]
}
