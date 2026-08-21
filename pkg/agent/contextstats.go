package agent

import (
	"encoding/json"
	"sort"
)

type ToolStat struct {
	Name   string
	Tokens int
}

// ContextStats estimates what occupies the model's context window, by
// category. Token counts are byte-based approximations (~4 bytes/token);
// LastInputTokens is the provider-reported figure for the latest request.
type ContextStats struct {
	Model  string
	Window int

	InstructionsTokens int
	ToolsTokens        int
	ToolStats          []ToolStat
	MessagesTokens     int
	MessageCount       int

	LastInputTokens int64
}

func (s ContextStats) EstimatedTotal() int {
	return s.InstructionsTokens + s.ToolsTokens + s.MessagesTokens
}

func (a *Agent) ContextStats() ContextStats {
	stats := ContextStats{}

	if a.Config != nil {
		if a.Model != nil {
			stats.Model = a.Model()
		}

		stats.Window = a.ContextWindow
		if stats.Window <= 0 {
			stats.Window = ContextWindowFor(stats.Model)
		}

		if a.Instructions != nil {
			stats.InstructionsTokens = estimateTokens(len(a.Instructions()))
		}

		if a.Tools != nil {
			for _, t := range a.Tools() {
				size := len(t.Name) + len(t.Description)
				if params, err := json.Marshal(t.Parameters); err == nil {
					size += len(params)
				}
				tokens := estimateTokens(size)
				stats.ToolsTokens += tokens
				stats.ToolStats = append(stats.ToolStats, ToolStat{Name: t.Name, Tokens: tokens})
			}
			sort.Slice(stats.ToolStats, func(i, j int) bool {
				return stats.ToolStats[i].Tokens > stats.ToolStats[j].Tokens
			})
		}
	}

	for _, m := range a.requestMessages() {
		stats.MessageCount++
		stats.MessagesTokens += estimateTokens(messageBytes(m))
	}

	stats.LastInputTokens = a.UsageSnapshot().LastInputTokens

	return stats
}

func estimateTokens(bytes int) int {
	return bytes / 4
}
