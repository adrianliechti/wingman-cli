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

// fsTools take a `path` arg that is workspace-relative; we display it with a
// leading "/" so it's visually distinct as a workspace path rather than a
// loose identifier.
var fsTools = map[string]bool{
	"read": true, "write": true, "edit": true,
	"ls": true, "find": true, "grep": true,
}

// workingDirTools default to the workspace root when their path arg is empty
// or ".". They render as "/" in that case.
var workingDirTools = map[string]bool{
	"ls": true, "find": true, "grep": true,
}

// ExtractToolHint pulls a short display hint out of a tool's JSON args.
// Prefers a "description" field; otherwise falls back to a priority list of
// common keys. For fs tools, paths are normalized to workspace-rooted form
// ("pkg/code" → "/pkg/code", "." → "/"). toolName may be empty if unknown.
func ExtractToolHint(argsJSON, toolName string) string {
	var args map[string]any

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return wdFallback(toolName)
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
		val, ok := args[key]
		if !ok {
			continue
		}
		str, ok := val.(string)
		if !ok || str == "" {
			continue
		}
		normalized := strings.Join(strings.Fields(str), " ")
		if (key == "path" || key == "file") && fsTools[toolName] {
			normalized = NormalizeWorkspacePath(normalized)
		}
		return normalized
	}

	return wdFallback(toolName)
}

// NormalizeWorkspacePath rewrites a workspace-relative path so that it always
// starts with "/". The cwd literals "." and "./" become "/". Already-absolute
// paths (starting with "/" or "~") pass through unchanged.
func NormalizeWorkspacePath(p string) string {
	if p == "" || p == "." || p == "./" {
		return "/"
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return p
	}
	return "/" + p
}

func wdFallback(toolName string) string {
	if workingDirTools[toolName] {
		return "/"
	}
	return ""
}
