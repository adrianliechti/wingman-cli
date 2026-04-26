package tui

import (
	"encoding/json"
	"fmt"
	"strings"
)

const Logo = `
[#84a0c6]██╗    ██╗[#89b8c2]██╗[#b4be82]███╗   ██╗[#e2a478] ██████╗ [#e27878]███╗   ███╗[#a093c7] █████╗ [#91acd1]███╗   ██╗
[#84a0c6]██║    ██║[#89b8c2]██║[#b4be82]████╗  ██║[#e2a478]██╔════╝ [#e27878]████╗ ████║[#a093c7]██╔══██╗[#91acd1]████╗  ██║
[#84a0c6]██║ █╗ ██║[#89b8c2]██║[#b4be82]██╔██╗ ██║[#e2a478]██║  ███╗[#e27878]██╔████╔██║[#a093c7]███████║[#91acd1]██╔██╗ ██║
[#84a0c6]██║███╗██║[#89b8c2]██║[#b4be82]██║╚██╗██║[#e2a478]██║   ██║[#e27878]██║╚██╔╝██║[#a093c7]██╔══██║[#91acd1]██║╚██╗██║
[#84a0c6]╚███╔███╔╝[#89b8c2]██║[#b4be82]██║ ╚████║[#e2a478]╚██████╔╝[#e27878]██║ ╚═╝ ██║[#a093c7]██║  ██║[#91acd1]██║ ╚████║
[#84a0c6] ╚══╝╚══╝ [#89b8c2]╚═╝[#b4be82]╚═╝  ╚═══╝[#e2a478] ╚═════╝ [#e27878]╚═╝     ╚═╝[#a093c7]╚═╝  ╚═╝[#91acd1]╚═╝  ╚═══╝[-]
`

// FormatTokens renders a token count as a short human-readable string:
// "1.5M" / "1.5K" / "42".
func FormatTokens(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}

	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}

	return fmt.Sprintf("%d", n)
}

// ExtractToolHint pulls a short display hint out of a tool's JSON args.
// Prefers a "description" field; otherwise falls back to a priority list of
// common keys. Returns "" if no usable string is found.
func ExtractToolHint(argsJSON string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}

	if desc, ok := args["description"]; ok {
		if str, ok := desc.(string); ok && str != "" {
			return strings.Join(strings.Fields(str), " ")
		}
	}

	hintKeys := []string{
		"query",
		"pattern",
		"command",
		"prompt",
		"path",
		"file",
		"url",
		"name",
	}

	for _, key := range hintKeys {
		if val, ok := args[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return strings.Join(strings.Fields(str), " ")
			}
		}
	}

	return ""
}
