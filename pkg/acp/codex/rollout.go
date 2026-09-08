package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/internal/pathutil"
)

func rolloutCommandOutputs(sessionID, explicitPath string) map[string]string {
	if out := readRolloutFile(explicitPath); out != nil {
		return out
	}
	if sessionID == "" {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return readRolloutFile(findRolloutFile(filepath.Join(home, ".codex", "sessions"), sessionID))
}

func readRolloutFile(path string) map[string]string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := parseRolloutOutputs(f)
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseRolloutOutputs(r io.Reader) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var line struct {
			Payload struct {
				Type   string          `json:"type"`
				CallID string          `json:"call_id"`
				Output json.RawMessage `json:"output"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		p := line.Payload
		if p.Type != "function_call_output" || p.CallID == "" {
			continue
		}
		if text := decodeRolloutOutput(p.Output); text != "" {
			out[p.CallID] = text
		}
	}
	return out
}

func findRolloutFile(root, sessionID string) string {
	suffix := sessionID + ".jsonl"
	var found string
	_ = pathutil.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	return found
}

func decodeRolloutOutput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		s = string(raw)
	}
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "Chunk ID:") || strings.HasPrefix(s, "Wall time:") {
		if i := strings.Index(s, "\nOutput:\n"); i >= 0 {
			s = s[i+len("\nOutput:\n"):]
		}
	}

	if text, ok := flattenContentBlocks(s); ok {
		return text
	}
	return s
}

func flattenContentBlocks(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "[") {
		return "", false
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(t), &blocks) != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}
