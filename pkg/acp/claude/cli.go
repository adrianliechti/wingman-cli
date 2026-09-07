package claude

import (
	"bufio"
	"encoding/json"
	"io"

	"github.com/coder/acp-go-sdk"
)

const maxCLIMessageSize = 32 * 1024 * 1024

func newCLIScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxCLIMessageSize)
	return scanner
}

func mcpConfigJSON(servers []acp.McpServer) string {
	if len(servers) == 0 {
		return ""
	}
	out := map[string]map[string]any{}
	for _, s := range servers {
		switch {
		case s.Stdio != nil:
			env := map[string]string{}
			for _, e := range s.Stdio.Env {
				env[e.Name] = e.Value
			}
			out[s.Stdio.Name] = map[string]any{"type": "stdio", "command": s.Stdio.Command, "args": s.Stdio.Args, "env": env}
		case s.Http != nil:
			out[s.Http.Name] = map[string]any{"type": "http", "url": s.Http.Url, "headers": headerMap(s.Http.Headers)}
		case s.Sse != nil:
			out[s.Sse.Name] = map[string]any{"type": "sse", "url": s.Sse.Url, "headers": headerMap(s.Sse.Headers)}
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, err := json.Marshal(map[string]any{"mcpServers": out})
	if err != nil {
		return ""
	}
	return string(b)
}

func headerMap(headers []acp.HttpHeader) map[string]string {
	m := map[string]string{}
	for _, h := range headers {
		m[h.Name] = h.Value
	}
	return m
}

type cliEnvelope struct {
	Type               string          `json:"type"`
	Subtype            string          `json:"subtype,omitempty"`
	Message            json.RawMessage `json:"message,omitempty"`
	Event              json.RawMessage `json:"event,omitempty"`
	Content            json.RawMessage `json:"content,omitempty"`
	State              string          `json:"state,omitempty"`
	Status             string          `json:"status,omitempty"`
	CompactResult      string          `json:"compact_result,omitempty"`
	SessionID          string          `json:"session_id,omitempty"`
	OriginalModel      string          `json:"original_model,omitempty"`
	FallbackModel      string          `json:"fallback_model,omitempty"`
	Direction          string          `json:"direction,omitempty"`
	RefusalCategory    string          `json:"api_refusal_category,omitempty"`
	RefusalExplanation string          `json:"api_refusal_explanation,omitempty"`
	ParentToolUseID    string          `json:"parent_tool_use_id,omitempty"`
	TaskID             string          `json:"task_id,omitempty"`
	ToolUseID          string          `json:"tool_use_id,omitempty"`
	ToolName           string          `json:"tool_name,omitempty"`
	ElapsedTimeSeconds float64         `json:"elapsed_time_seconds,omitempty"`
	SubagentType       string          `json:"subagent_type,omitempty"`
	SubagentRetry      map[string]any  `json:"subagent_retry,omitempty"`
	Description        string          `json:"description,omitempty"`
	LastToolName       string          `json:"last_tool_name,omitempty"`
	Summary            string          `json:"summary,omitempty"`

	// The only signal a misconfigured server or plugin produces; stderr stays empty when captured.
	MCPServers      []cliNamedStatus `json:"mcp_servers,omitempty"`
	MCPServerErrors []cliLoadError   `json:"mcp_server_errors,omitempty"`
	PluginErrors    []cliLoadError   `json:"plugin_errors,omitempty"`

	// rate_limit_event
	ResetsAt      any    `json:"resetsAt,omitempty"`
	RateLimitType string `json:"rateLimitType,omitempty"`
	Patch         struct {
		Status string `json:"status,omitempty"`
	} `json:"patch"`
}

type cliNamedStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// cliLoadError covers mcp_server_errors (`name`) and plugin_errors (`plugin`).
type cliLoadError struct {
	Name    string `json:"name"`
	Plugin  string `json:"plugin"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e cliLoadError) label() string {
	if e.Name != "" {
		return e.Name
	}
	return e.Plugin
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID string `json:"id"`
	} `json:"message"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		Thinking string `json:"thinking,omitempty"`
	} `json:"delta"`
}

type cliMessage struct {
	ID      string        `json:"id"`
	Content []cliMsgBlock `json:"content"`
}

type cliMsgBlock struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     json.RawMessage  `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   json.RawMessage  `json:"content,omitempty"`
	IsError   bool             `json:"is_error,omitempty"`
	Title     string           `json:"title,omitempty"`
	Source    *cliResultSource `json:"source,omitempty"`
}

type cliResult struct {
	UserMessageUUID string                   `json:"user_message_uuid,omitempty"`
	Subtype         string                   `json:"subtype"`
	StopReason      string                   `json:"stop_reason"`
	IsError         bool                     `json:"is_error"`
	Result          string                   `json:"result"`
	Errors          []string                 `json:"errors"`
	Usage           *cliUsage                `json:"usage,omitempty"`
	ModelUsage      map[string]cliModelUsage `json:"modelUsage,omitempty"`
	TotalCostUSD    float64                  `json:"total_cost_usd,omitempty"`
}

type cliUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type cliModelUsage struct {
	ContextWindow int `json:"contextWindow"`
}

type cliInput struct {
	Type    string          `json:"type"`
	UUID    string          `json:"uuid,omitempty"`
	Message cliInputMessage `json:"message"`
}

type cliInputMessage struct {
	Role    string            `json:"role"`
	Content []cliInputContent `json:"content"`
}

type cliInputContent struct {
	Type   string               `json:"type"`
	Text   string               `json:"text,omitempty"`
	Source *cliInputImageSource `json:"source,omitempty"`
}

type cliInputImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type cliResultSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type controlRequest struct {
	RequestID string             `json:"request_id"`
	AgentID   string             `json:"agent_id,omitempty"`
	Request   controlRequestBody `json:"request"`
}

type controlRequestBody struct {
	Subtype               string          `json:"subtype"`
	ToolName              string          `json:"tool_name"`
	ToolUseID             string          `json:"tool_use_id"`
	AgentID               string          `json:"agent_id,omitempty"`
	Input                 json.RawMessage `json:"input"`
	Description           string          `json:"description"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
}

type controlResponse struct {
	Type     string              `json:"type"`
	Response controlResponseBody `json:"response"`
}

type controlInterrupt struct {
	Type      string               `json:"type"`
	RequestID string               `json:"request_id"`
	Request   controlInterruptBody `json:"request"`
}

type controlInterruptBody struct {
	Subtype string `json:"subtype"`
}

type controlResponseBody struct {
	Subtype   string `json:"subtype"`
	RequestID string `json:"request_id"`
	Response  any    `json:"response,omitempty"`
	Error     string `json:"error,omitempty"`
}
