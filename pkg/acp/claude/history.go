package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
)

const maxProjectKeyLen = 200

func projectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func encodeCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	b := make([]byte, len(cwd))
	for i := 0; i < len(cwd); i++ {
		c := cwd[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b[i] = c
		default:
			b[i] = '-'
		}
	}
	if len(b) > maxProjectKeyLen {

		b = b[:maxProjectKeyLen]
	}
	return string(b)
}

func projectDirFor(cwd string) string {
	if cwd == "" {
		return ""
	}
	root := projectsRoot()
	if root == "" {
		return ""
	}
	resolved := cwd
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		resolved = r
	}
	return filepath.Join(root, encodeCwd(resolved))
}

type historyHeader struct {
	Type    string               `json:"type"`
	AITitle string               `json:"aiTitle,omitempty"`
	Cwd     string               `json:"cwd,omitempty"`
	Message historyHeaderMessage `json:"message"`
}

type historyHeaderMessage struct {
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"`
	Model   string          `json:"model,omitempty"`
}

func scanSessionModel(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var model string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var h historyHeader
		if json.Unmarshal(scanner.Bytes(), &h) == nil && h.Type == "assistant" && h.Message.Model != "" {
			model = h.Message.Model
		}
	}
	_ = scanner.Err()
	return model
}

func listProjectSessions(cwd string) ([]acp.SessionInfo, error) {
	dirs, err := projectDirs(cwd)
	if err != nil {
		return nil, err
	}
	out := make([]acp.SessionInfo, 0)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			title, recordedCwd := scanSessionMetadata(filepath.Join(dir, name))
			sessCwd := recordedCwd
			if sessCwd == "" {
				sessCwd = cwd
			}
			if sessCwd == "" {

				continue
			}
			ts := info.ModTime().UTC().Format(time.RFC3339)
			si := acp.SessionInfo{
				SessionId: acp.SessionId(id),
				Cwd:       sessCwd,
				UpdatedAt: &ts,
			}
			if title != "" {
				si.Title = &title
			}
			out = append(out, si)
		}
	}
	sort.Slice(out, func(i, j int) bool {

		return *out[i].UpdatedAt > *out[j].UpdatedAt
	})
	return out, nil
}

func projectDirs(cwd string) ([]string, error) {
	if cwd != "" {
		d := projectDirFor(cwd)
		if d == "" {
			return nil, nil
		}
		return []string{d}, nil
	}
	root := projectsRoot()
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(root, e.Name()))
	}
	return out, nil
}

func deleteProjectSession(id acp.SessionId) error {
	dirs, err := projectDirs("")
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := os.Remove(filepath.Join(dir, string(id)+".jsonl")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func sessionExists(cwd string, id acp.SessionId) bool {
	dir := projectDirFor(cwd)
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, string(id)+".jsonl"))
	return err == nil
}

func scanSessionMetadata(path string) (title, cwd string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var firstUserText, latestAITitle string
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var h historyHeader
		if err := json.Unmarshal(line, &h); err != nil {
			continue
		}
		if cwd == "" && h.Cwd != "" {
			cwd = h.Cwd
		}
		switch h.Type {
		case "ai-title":
			// The CLI appends a fresh ai-title line as the conversation
			// evolves rather than rewriting the old one, so keep scanning
			// and remember the latest rather than returning the first.
			if h.AITitle != "" {
				latestAITitle = h.AITitle
			}
		case "user":
			if firstUserText == "" {
				if text := firstTextFromContent(h.Message.Content); text != "" {
					firstUserText = text
				}
			}
		}
	}
	_ = scanner.Err()
	if latestAITitle != "" {
		return latestAITitle, cwd
	}
	if firstUserText != "" {
		return truncateTitle(firstUserText), cwd
	}
	return "", cwd
}

func firstTextFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []cliMsgBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

func truncateTitle(s string) string {
	const max = 80
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func replayHistory(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, cwd string, plan *taskPlan) error {
	dir := projectDirFor(cwd)
	if dir == "" {
		return nil
	}
	path := filepath.Join(dir, string(sid)+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	return streamHistory(ctx, conn, sid, cwd, f, plan)
}

func streamHistory(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, cwd string, r io.Reader, plan *taskPlan) error {
	cache := toolUseCache{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env cliEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		switch env.Type {
		case "user":
			if err := replayUserMessage(ctx, conn, sid, env.Message, cache, plan, env.ParentToolUseID); err != nil {
				return err
			}
		case "assistant":
			if err := emitAssistant(ctx, conn, sid, env.Message, cwd, cache, nil, nil, plan, env.ParentToolUseID); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan history: %w", err)
	}
	return nil
}

var localCommandStdoutPattern = regexp.MustCompile(`(?s)<local-command-stdout>(.*?)</local-command-stdout>`)

var localCommandTagPattern = regexp.MustCompile(`(?s)<command-name>.*?</command-name>|<command-message>.*?</command-message>|<command-args>.*?</command-args>|<local-command-stderr>.*?</local-command-stderr>`)

func stripMarkerTags(text string) (string, bool) {
	stripped := localCommandStdoutPattern.ReplaceAllString(text, "$1")
	stripped = localCommandTagPattern.ReplaceAllString(stripped, "")
	if strings.TrimSpace(stripped) == "" {
		return "", false
	}
	return stripped, true
}

func replayUserMessage(ctx context.Context, conn *acp.AgentSideConnection, sid acp.SessionId, raw json.RawMessage, cache toolUseCache, plan *taskPlan, parentToolUseID string) error {
	if len(raw) == 0 {
		return nil
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}

	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		if parentToolUseID != "" {
			return nil
		}
		text, ok := stripMarkerTags(s)
		if !ok {
			return nil
		}
		return conn.SessionUpdate(ctx, acp.SessionNotification{
			SessionId: sid,
			Update:    acp.UpdateUserMessageText(text),
		})
	}

	var blocks []cliMsgBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if parentToolUseID != "" {
				continue
			}
			text, ok := stripMarkerTags(b.Text)
			if !ok {
				continue
			}
			if err := conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: sid,
				Update:    acp.UpdateUserMessageText(text),
			}); err != nil {
				return err
			}
		case "tool_result":
			if b.ToolUseID == "" {
				continue
			}
			name := cache[b.ToolUseID]
			if applyTaskPlanResult(plan, name, b) {
				if err := conn.SessionUpdate(ctx, acp.SessionNotification{
					SessionId: sid,
					Update:    acp.UpdatePlan(plan.entries()...),
				}); err != nil {
					return err
				}
			}
			if isPlanTool(name) {
				continue
			}
			status := acp.ToolCallStatusCompleted
			if b.IsError {
				status = acp.ToolCallStatusFailed
			}
			opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
			if content := toolResultContent(name, b); len(content) > 0 {
				opts = append(opts, acp.WithUpdateContent(content))
			}
			u := acp.UpdateToolCall(acp.ToolCallId(b.ToolUseID), opts...)
			withClaudeToolMeta(&u, name, parentToolUseID)
			if err := conn.SessionUpdate(ctx, acp.SessionNotification{
				SessionId: sid,
				Update:    u,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
