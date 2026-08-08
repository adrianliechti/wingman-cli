package pi

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

const (
	headBytes = 64 * 1024
	tailBytes = 256 * 1024
)

type sessionFileInfo struct {
	ID        string
	Cwd       string
	Title     string
	UpdatedAt string
	Path      string
}

func listSessionFiles(sessionsDir string) []sessionFileInfo {
	var files []string
	_ = filepath.WalkDir(sessionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})

	var out []sessionFileInfo
	for _, file := range files {
		first := readFirstLine(file)
		id, cwd, ok := parseSessionHeader(first)
		if !ok {
			continue
		}

		info := sessionFileInfo{ID: id, Cwd: cwd, Path: file}

		tail := readTail(file, tailBytes)
		info.Title = pickTitleFromTail(tail)
		info.UpdatedAt = pickUpdatedAtFromTail(tail)

		if info.UpdatedAt == "" {
			if st, err := os.Stat(file); err == nil {
				info.UpdatedAt = st.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
			}
		}
		if info.Title == "" {
			info.Title = pickFirstUserMessage(file)
		}

		out = append(out, info)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func findSessionFile(sessionsDir, sessionID string) (sessionFileInfo, bool) {
	for _, s := range listSessionFiles(sessionsDir) {
		if s.ID == sessionID {
			return s, true
		}
	}
	return sessionFileInfo{}, false
}

func readFirstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, headBytes)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func readTail(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return ""
	}
	start := max(st.Size()-n, 0)
	if _, err := f.Seek(start, 0); err != nil {
		return ""
	}
	data, _ := io.ReadAll(f)
	return string(data)
}

func parseSessionHeader(line string) (id, cwd string, ok bool) {
	if line == "" {
		return "", "", false
	}
	var h struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Cwd  string `json:"cwd"`
	}
	if json.Unmarshal([]byte(line), &h) != nil || h.Type != "session" {
		return "", "", false
	}
	if h.ID == "" || h.Cwd == "" {
		return "", "", false
	}
	return h.ID, h.Cwd, true
}

func pickTitleFromTail(tail string) string {
	lines := strings.Split(tail, "\n")
	for _, line := range slices.Backward(lines) {
		line := strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(line), &obj) == nil && obj.Type == "session_info" && strings.TrimSpace(obj.Name) != "" {
			return strings.TrimSpace(obj.Name)
		}
	}
	return ""
}

func pickUpdatedAtFromTail(tail string) string {
	lines := strings.Split(tail, "\n")
	for _, line := range slices.Backward(lines) {
		line := strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal([]byte(line), &obj) == nil && obj.Type == "message" && obj.Timestamp != "" {
			return obj.Timestamp
		}
	}
	return ""
}

func pickFirstUserMessage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	count := 0
	for scanner.Scan() {
		count++
		if count > 2000 {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &obj) != nil || obj.Type != "message" || obj.Message.Role != "user" {
			continue
		}
		if text := normalizePiText(obj.Message.Content); text != "" {
			return truncate(text, 80)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func normalizePiText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, c := range blocks {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func replayMessages(send func(acp.SessionUpdate), data json.RawMessage) {
	var d struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &d) != nil {
		return
	}

	startedTools := make(map[string]bool)
	for _, raw := range d.Messages {
		var m struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolName   string          `json:"toolName"`
			ToolCallID string          `json:"toolCallId"`
			IsError    bool            `json:"isError"`
			Args       json.RawMessage `json:"args"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}

		switch m.Role {
		case "user":
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				if text != "" {
					send(acp.UpdateUserMessage(acp.TextBlock(text)))
				}
				continue
			}
			for _, block := range parsePiMessageContent(m.Content) {
				switch block.Type {
				case "text":
					if block.Text != "" {
						send(acp.UpdateUserMessage(acp.TextBlock(block.Text)))
					}
				case "image":
					if block.Data != "" {
						send(acp.UpdateUserMessage(acp.ImageBlock(block.Data, block.MimeType)))
					}
				}
			}

		case "assistant":
			for _, block := range parsePiMessageContent(m.Content) {
				switch block.Type {
				case "text":
					if block.Text != "" {
						send(acp.UpdateAgentMessageText(block.Text))
					}
				case "thinking":
					if block.Thinking != "" {
						send(acp.UpdateAgentThoughtText(block.Thinking))
					}
				case "toolCall":
					if block.ID == "" {
						continue
					}
					startedTools[block.ID] = true
					opts := []acp.ToolCallStartOpt{
						acp.WithStartKind(toolKind(block.Name)),
						acp.WithStartStatus(acp.ToolCallStatusPending),
					}
					if block.Arguments != nil {
						opts = append(opts, acp.WithStartRawInput(block.Arguments))
					}
					send(acp.StartToolCall(acp.ToolCallId(block.ID), toolTitle(block.Name, block.Arguments), opts...))
				}
			}

		case "toolResult":
			id := m.ToolCallID
			if id == "" {
				id = uuid.NewString()
			}
			name := m.ToolName
			if name == "" {
				name = "tool"
			}
			status := acp.ToolCallStatusCompleted
			if m.IsError {
				status = acp.ToolCallStatusFailed
			}
			var args map[string]any
			_ = json.Unmarshal(m.Args, &args)
			if !startedTools[id] {
				send(acp.StartToolCall(acp.ToolCallId(id), toolTitle(name, args),
					acp.WithStartKind(toolKind(name)),
					acp.WithStartStatus(status),
				))
			}
			opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
			if content := piToolResultContent(m.Content); len(content) > 0 {
				opts = append(opts, acp.WithUpdateContent(content))
			}
			send(acp.UpdateToolCall(acp.ToolCallId(id), opts...))
			delete(startedTools, id)
		}
	}
}

type piMessageContent struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Thinking  string         `json:"thinking"`
	Data      string         `json:"data"`
	MimeType  string         `json:"mimeType"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func parsePiMessageContent(content json.RawMessage) []piMessageContent {
	var blocks []piMessageContent
	_ = json.Unmarshal(content, &blocks)
	return blocks
}

func piToolResultContent(content json.RawMessage) []acp.ToolCallContent {
	var out []acp.ToolCallContent
	for _, block := range parsePiMessageContent(content) {
		switch block.Type {
		case "text":
			if block.Text != "" {
				out = append(out, acp.ToolContent(acp.TextBlock(block.Text)))
			}
		case "image":
			if block.Data != "" {
				out = append(out, acp.ToolContent(acp.ImageBlock(block.Data, block.MimeType)))
			}
		}
	}
	return out
}
