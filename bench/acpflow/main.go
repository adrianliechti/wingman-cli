package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const taskPrompt = `Fix the invoice-total bug reported in this repository: tax must be calculated on the discounted subtotal, not on the original subtotal. Begin by searching for the relevant symbols, inspect the implementation and focused tests, make the smallest correct code change, and run the focused test. Do not use subagents. Finish with a concise summary.`

const denseTaskSuffix = ` Do not use the todo tool. Minimize model round trips: batch independent searches and reads in one response, make the edit once you have enough evidence, and combine final verification commands where safe.`

const dashboardCreatePrompt = `This is a freshly scaffolded Vite project with its React, TypeScript, Tailwind CSS, and Lucide dependencies already installed. Do not install or upgrade packages. Build a polished responsive personal-finance dashboard. Include a collapsible-feeling sidebar, header actions, four KPI cards, an SVG or CSS cash-flow chart, recent transactions, and budget progress. Use realistic static data, semantic accessible markup, Lucide icons, and a restrained professional visual system. Create the needed source files and run npm run build. Do not use subagents, optional skills, browser automation, screenshots, or a development server; implement directly and validate with one production build.`

const dashboardModifyPrompt = `Modify the existing finance dashboard. First inspect the current implementation. Add an accessible 7D / 30D / 90D range selector to the cash-flow chart, defaulting to 30D; changing it must update both the plotted data and the visible period label while preserving the existing style and responsive behavior. Run npm run build, then summarize the change. Do not use subagents, optional skills, browser automation, screenshots, or a development server; implement directly and validate with one production build.`

type timedEvent struct {
	AtMS    int64  `json:"at_ms"`
	Kind    string `json:"kind"`
	ID      string `json:"id,omitempty"`
	Title   string `json:"title,omitempty"`
	Status  string `json:"status,omitempty"`
	Preview string `json:"preview,omitempty"`
}

type benchClient struct {
	mu          sync.Mutex
	start       time.Time
	events      []timedEvent
	text        strings.Builder
	thoughtSeen bool
	messageSeen bool
}

func (c *benchClient) reset(start time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.start = start
	c.events = nil
	c.text.Reset()
	c.thoughtSeen = false
	c.messageSeen = false
}

func (c *benchClient) add(event timedEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.start.IsZero() {
		return
	}
	event.AtMS = time.Since(c.start).Milliseconds()
	c.events = append(c.events, event)
}

func preview(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 180 {
		return value[:180] + "…"
	}
	return value
}

func (c *benchClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	u := n.Update
	switch {
	case u.AgentThoughtChunk != nil:
		if block := u.AgentThoughtChunk.Content.Text; block != nil {
			c.mu.Lock()
			seen := c.thoughtSeen
			c.thoughtSeen = true
			c.mu.Unlock()
			if !seen {
				c.add(timedEvent{Kind: "first_thought", Preview: preview(block.Text)})
			}
		}
	case u.AgentMessageChunk != nil:
		if block := u.AgentMessageChunk.Content.Text; block != nil {
			c.mu.Lock()
			c.text.WriteString(block.Text)
			seen := c.messageSeen
			c.messageSeen = true
			c.mu.Unlock()
			if !seen {
				c.add(timedEvent{Kind: "first_message", Preview: preview(block.Text)})
			}
		}
	case u.ToolCall != nil:
		c.add(timedEvent{
			Kind: "tool_start", ID: string(u.ToolCall.ToolCallId), Title: u.ToolCall.Title,
			Status: string(u.ToolCall.Status), Preview: preview(fmt.Sprint(u.ToolCall.RawInput)),
		})
	case u.ToolCallUpdate != nil:
		status := ""
		if u.ToolCallUpdate.Status != nil {
			status = string(*u.ToolCallUpdate.Status)
		}
		title := ""
		if u.ToolCallUpdate.Title != nil {
			title = *u.ToolCallUpdate.Title
		}
		c.add(timedEvent{
			Kind: "tool_update", ID: string(u.ToolCallUpdate.ToolCallId), Title: title,
			Status: status, Preview: preview(fmt.Sprint(u.ToolCallUpdate.RawOutput)),
		})
	}
	return nil
}

func (c *benchClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if len(p.Options) == 0 {
		return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Selected: &acp.RequestPermissionOutcomeSelected{OptionId: p.Options[0].OptionId},
	}}, nil
}

func (*benchClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.ErrUnsupported
}
func (*benchClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.ErrUnsupported
}
func (*benchClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.ErrUnsupported
}
func (*benchClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.ErrUnsupported
}
func (*benchClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.ErrUnsupported
}
func (*benchClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.ErrUnsupported
}
func (*benchClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.ErrUnsupported
}

func (c *benchClient) snapshot() ([]timedEvent, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]timedEvent(nil), c.events...), c.text.String()
}

type benchResult struct {
	Agent        string       `json:"agent"`
	Model        string       `json:"model"`
	Effort       string       `json:"effort"`
	InitMS       int64        `json:"initialize_ms"`
	NewSessionMS int64        `json:"new_session_ms"`
	TotalMS      int64        `json:"prompt_total_ms"`
	StopReason   string       `json:"stop_reason"`
	Usage        *acp.Usage   `json:"usage,omitempty"`
	Events       []timedEvent `json:"events"`
	FinalText    string       `json:"final_text"`
	File         string       `json:"invoice_file"`
	TestsPassed  bool         `json:"tests_passed"`
	AgentStderr  string       `json:"agent_stderr,omitempty"`
	Error        string       `json:"error,omitempty"`
	Scenario     string       `json:"scenario,omitempty"`
	Turns        []benchTurn  `json:"turns,omitempty"`
}

type benchTurn struct {
	Name               string       `json:"name"`
	TotalMS            int64        `json:"prompt_total_ms"`
	StopReason         string       `json:"stop_reason"`
	Usage              *acp.Usage   `json:"usage,omitempty"`
	Events             []timedEvent `json:"events"`
	FinalText          string       `json:"final_text"`
	BuildPassed        bool         `json:"build_passed"`
	RequirementsPassed bool         `json:"requirements_passed"`
	FileCount          int          `json:"source_file_count"`
	BuildOutput        string       `json:"build_output,omitempty"`
	Error              string       `json:"error,omitempty"`
}

func writeFixture(root string) error {
	if err := os.MkdirAll(filepath.Join(root, "billing"), 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"go.mod":    "module example.com/invoice\n\ngo 1.26\n",
		"README.md": "# Invoice sample\n\nThe billing package computes invoice totals.\n",
		"billing/invoice.go": `package billing

// Invoice stores monetary values in cents.
type Invoice struct {
	SubtotalCents int
	DiscountCents int
	TaxPercent    int
}

// TotalCents returns the amount due after discount and tax.
func TotalCents(invoice Invoice) int {
	discounted := invoice.SubtotalCents - invoice.DiscountCents
	tax := invoice.SubtotalCents * invoice.TaxPercent / 100
	return discounted + tax
}
`,
		"billing/invoice_test.go": `package billing

import "testing"

func TestTotalCentsTaxesDiscountedSubtotal(t *testing.T) {
	invoice := Invoice{SubtotalCents: 1000, DiscountCents: 200, TaxPercent: 10}
	if got, want := TotalCents(invoice), 880; got != want {
		t.Fatalf("TotalCents() = %d, want %d", got, want)
	}
}
`,
		"shipping/rates.go": `package shipping

func Total(weightGrams int) int { return 500 + weightGrams/10 }
`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	return cmd.Run()
}

func writeDashboardFixture(root, nodeModules string) error {
	files := map[string]string{
		"package.json": `{
  "name": "finance-dashboard",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": { "build": "tsc -b && vite build" },
  "dependencies": {
    "@tailwindcss/vite": "^4.3.3",
    "lucide-react": "^1.31.0",
    "react": "^19.2.8",
    "react-dom": "^19.2.8",
    "tailwindcss": "^4.3.3"
  },
  "devDependencies": {
    "@types/node": "^26.2.0",
    "@types/react": "^19.2.18",
    "@types/react-dom": "^19.2.4",
    "@vitejs/plugin-react": "^6.0.5",
    "typescript": "^7.0.2",
    "vite": "^8.2.0"
  }
}
`,
		"index.html": `<!doctype html>
<html lang="en"><head><meta charset="UTF-8" /><meta name="viewport" content="width=device-width, initial-scale=1.0" /><title>Ledger Finance</title></head><body><div id="root"></div><script type="module" src="/src/main.tsx"></script></body></html>
`,
		"vite.config.ts": `import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({ plugins: [react(), tailwindcss()] });
`,
		"tsconfig.json": `{
  "files": [],
  "references": [{ "path": "./tsconfig.app.json" }, { "path": "./tsconfig.node.json" }]
}
`,
		"tsconfig.app.json": `{
  "compilerOptions": {
    "tsBuildInfoFile": "./.tmp/tsconfig.app.tsbuildinfo",
    "target": "ES2023", "lib": ["ES2023", "DOM", "DOM.Iterable"],
    "module": "ESNext", "types": ["vite/client"], "skipLibCheck": true,
    "moduleResolution": "bundler", "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true, "moduleDetection": "force", "noEmit": true,
    "jsx": "react-jsx", "noUnusedLocals": true, "noUnusedParameters": true,
    "erasableSyntaxOnly": true, "noFallthroughCasesInSwitch": true
  },
  "include": ["src"]
}
`,
		"tsconfig.node.json": `{
  "compilerOptions": {
    "tsBuildInfoFile": "./.tmp/tsconfig.node.tsbuildinfo",
    "target": "ES2023", "lib": ["ES2023"], "module": "ESNext", "types": ["node"],
    "skipLibCheck": true, "moduleResolution": "bundler", "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true, "moduleDetection": "force", "noEmit": true,
    "noUnusedLocals": true, "noUnusedParameters": true, "erasableSyntaxOnly": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["vite.config.ts"]
}
`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(nodeModules, filepath.Join(root, "node_modules")); err != nil {
		return fmt.Errorf("link preinstalled node_modules: %w", err)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	return cmd.Run()
}

func replaceEnv(parent []string, values map[string]string) []string {
	out := make([]string, 0, len(parent)+len(values))
	for _, item := range parent {
		key, _, _ := strings.Cut(item, "=")
		if _, replace := values[key]; !replace {
			out = append(out, item)
		}
	}
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func appendBenchError(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}

func runDashboardTurn(ctx context.Context, conn *acp.ClientSideConnection, client *benchClient, sessionID acp.SessionId, root, name, prompt string) benchTurn {
	turn := benchTurn{Name: name}
	started := time.Now()
	client.reset(started)
	response, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})
	turn.TotalMS = time.Since(started).Milliseconds()
	turn.Events, turn.FinalText = client.snapshot()
	if err != nil {
		turn.Error = "prompt: " + err.Error()
	} else {
		turn.StopReason = string(response.StopReason)
		turn.Usage = response.Usage
	}
	build := exec.CommandContext(ctx, "npm", "run", "build")
	build.Dir = root
	build.Env = replaceEnv(os.Environ(), map[string]string{"NO_COLOR": "1"})
	buildOutput, buildErr := build.CombinedOutput()
	turn.BuildPassed = buildErr == nil
	if buildErr != nil {
		turn.BuildOutput = preview(string(buildOutput))
		turn.Error = appendBenchError(turn.Error, "build: "+buildErr.Error())
	}
	_ = filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			turn.FileCount++
		}
		return nil
	})
	if requirementErr := checkDashboardRequirements(root, name); requirementErr != nil {
		turn.Error = appendBenchError(turn.Error, "requirements: "+requirementErr.Error())
	} else {
		turn.RequirementsPassed = true
	}
	return turn
}

func checkDashboardRequirements(root, turn string) error {
	var source strings.Builder
	err := filepath.WalkDir(filepath.Join(root, "src"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source.Write(contents)
		source.WriteByte('\n')
		return nil
	})
	if err != nil {
		return err
	}
	contents := strings.ToLower(source.String())
	required := []string{"cash flow", "transaction", "budget"}
	if turn == "add_range_selector" {
		required = append(required, "7d", "30d", "90d")
	}
	var missing []string
	for _, value := range required {
		if !strings.Contains(contents, value) {
			missing = append(missing, value)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("source is missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func runOne(ctx context.Context, binary, agentName, model, effort, scenario, nodeModules, gatewayURL, otlpURL string) benchResult {
	result := benchResult{Agent: agentName, Model: model, Effort: effort, Scenario: scenario}
	fixture, err := os.MkdirTemp("", "harness-e2e-"+agentName+"-")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer os.RemoveAll(fixture)
	if scenario == "dashboard" {
		err = writeDashboardFixture(fixture, nodeModules)
	} else {
		err = writeFixture(fixture)
	}
	if err != nil {
		result.Error = err.Error()
		return result
	}
	wingmanHome, err := os.MkdirTemp("", "wingman-home-"+agentName+"-")
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer os.RemoveAll(wingmanHome)

	var args []string
	switch agentName {
	case "wingman":
		args = []string{"acp", "wingman"}
	case "codex":
		args = []string{"acp", "codex", "--backend", "wingman", "--model", model, "--effort", effort}
	case "claude":
		args = []string{"acp", "claude", "--backend", "wingman", "--model", model, "--effort", effort}
	default:
		result.Error = fmt.Sprintf("unknown agent %q (choose wingman, codex, or claude)", agentName)
		return result
	}
	cmd := exec.Command(binary, args...)
	configureChildProcess(cmd)
	cmd.Dir = fixture
	environment := map[string]string{
		"WINGMAN_URL":                                        gatewayURL,
		"WINGMAN_TOKEN":                                      "-",
		"WINGMAN_MODEL":                                      model,
		"WINGMAN_EFFORT":                                     effort,
		"WINGMAN_ELICITATION":                                "accept",
		"WINGMAN_HOME":                                       wingmanHome,
		"OTEL_TRACES_EXPORTER":                               "otlp",
		"OTEL_METRICS_EXPORTER":                              "otlp",
		"OTEL_LOGS_EXPORTER":                                 "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":                        "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":                        otlpURL,
		"OTEL_METRIC_EXPORT_INTERVAL":                        "5000",
		"OTEL_LOGS_EXPORT_INTERVAL":                          "5000",
		"OTEL_TRACES_EXPORT_INTERVAL":                        "5000",
		"OTEL_SERVICE_NAME":                                  "acpflow-" + agentName,
		"OTEL_RESOURCE_ATTRIBUTES":                           "benchmark.agent=" + agentName + ",benchmark.model=" + model,
		"OTEL_INSTRUMENTATION_GENAI_EMIT_EVENT":              "true",
		"OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT": "NO_CONTENT",
	}
	switch agentName {
	case "claude":
		for key, value := range claudeTelemetryEnvironment(otlpURL, model) {
			environment[key] = value
		}
	}
	cmd.Env = replaceEnv(os.Environ(), environment)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		result.Error = err.Error()
		return result
	}
	defer func() {
		_ = stdin.Close()
		exited := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(exited)
		}()
		select {
		case <-exited:
			// The ACP wrapper may exit before a CLI it spawned. Signal the now
			// parentless process group too, otherwise it can retain our pipes.
			_ = stopChildProcess(cmd)
		case <-time.After(10 * time.Second):
			_ = stopChildProcess(cmd)
			select {
			case <-exited:
			case <-time.After(3 * time.Second):
				_ = killChildProcess(cmd)
				select {
				case <-exited:
				case <-time.After(3 * time.Second):
				}
			}
		}
	}()

	client := &benchClient{}
	conn := acp.NewClientSideConnection(client, stdin, stdout)
	started := time.Now()
	_, err = conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "harness-bench", Version: "1"},
	})
	result.InitMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = "initialize: " + err.Error()
		result.AgentStderr = stderr.String()
		return result
	}
	started = time.Now()
	session, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: fixture, McpServers: []acp.McpServer{}})
	result.NewSessionMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = "new session: " + err.Error()
		result.AgentStderr = stderr.String()
		return result
	}
	if scenario == "dashboard" {
		result.Turns = append(result.Turns,
			runDashboardTurn(ctx, conn, client, session.SessionId, fixture, "create_dashboard", dashboardCreatePrompt),
			runDashboardTurn(ctx, conn, client, session.SessionId, fixture, "add_range_selector", dashboardModifyPrompt),
		)
		result.TestsPassed = len(result.Turns) > 0
		for _, turn := range result.Turns {
			result.TotalMS += turn.TotalMS
			if !turn.BuildPassed || !turn.RequirementsPassed {
				result.TestsPassed = false
			}
			if turn.Error != "" {
				result.TestsPassed = false
				result.Error = turn.Name + ": " + turn.Error
				break
			}
		}
		if len(result.Turns) > 0 {
			result.StopReason = result.Turns[len(result.Turns)-1].StopReason
		}
		if value := strings.TrimSpace(stderr.String()); value != "" {
			result.AgentStderr = value
		}
		return result
	}

	started = time.Now()
	client.reset(started)
	prompt := taskPrompt
	if scenario == "invoice_dense" {
		prompt += denseTaskSuffix
	}
	response, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: session.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	})
	result.TotalMS = time.Since(started).Milliseconds()
	result.Events, result.FinalText = client.snapshot()
	if err != nil {
		result.Error = "prompt: " + err.Error()
	} else {
		result.StopReason = string(response.StopReason)
		result.Usage = response.Usage
	}
	contents, readErr := os.ReadFile(filepath.Join(fixture, "billing", "invoice.go"))
	if readErr == nil {
		result.File = string(contents)
	}
	test := exec.CommandContext(ctx, "go", "test", "./billing")
	test.Dir = fixture
	test.Env = replaceEnv(os.Environ(), map[string]string{"GOCACHE": filepath.Join(fixture, ".gocache")})
	result.TestsPassed = test.Run() == nil
	if value := strings.TrimSpace(stderr.String()); value != "" {
		result.AgentStderr = value
	}
	return result
}

func claudeTelemetryEnvironment(endpoint, model string) map[string]string {
	return map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
		"CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
		"CLAUDE_CODE_OTEL_DIAG_STDERR":        "1",
		"OTEL_TRACES_EXPORTER":                "otlp",
		"OTEL_METRICS_EXPORTER":               "otlp",
		"OTEL_LOGS_EXPORTER":                  "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":         endpoint,
		"OTEL_METRIC_EXPORT_INTERVAL":         "5000",
		"OTEL_LOGS_EXPORT_INTERVAL":           "5000",
		"OTEL_TRACES_EXPORT_INTERVAL":         "5000",
		"OTEL_SERVICE_NAME":                   "acpflow-claude",
		"OTEL_RESOURCE_ATTRIBUTES":            "benchmark.agent=claude,benchmark.model=" + model,
		"OTEL_LOG_USER_PROMPTS":               "0",
		"OTEL_LOG_ASSISTANT_RESPONSES":        "0",
		"OTEL_LOG_TOOL_DETAILS":               "0",
		"OTEL_LOG_TOOL_CONTENT":               "0",
	}
}

func main() {
	binary := flag.String("binary", "/private/tmp/wingman-harness-bench", "Wingman binary")
	agentName := flag.String("agent", "wingman", "wingman, codex, or claude")
	model := flag.String("model", "gpt-5.6-sol", "model ID")
	effort := flag.String("effort", "high", "reasoning effort")
	scenario := flag.String("scenario", "invoice", "invoice, invoice_dense, or dashboard")
	nodeModules := flag.String("node-modules", "server/ui/node_modules", "preinstalled node_modules for dashboard")
	gateway := flag.String("gateway", "http://localhost:4242", "gateway base URL")
	timeout := flag.Duration("timeout", 12*time.Minute, "run timeout")
	output := flag.String("output", "", "optional JSON output path")
	telemetryOutput := flag.String("telemetry-output", "", "optional OTLP JSON sidecar path (defaults beside -output)")
	reportOutput := flag.String("report", "auto", "comparison HTML path; auto writes beside -output, none disables")
	reportOnly := flag.String("report-only", "", "generate comparison artifacts from an existing result directory without running an agent")
	flag.Parse()
	if *reportOnly != "" {
		directory, err := filepath.Abs(*reportOnly)
		if err != nil {
			panic(err)
		}
		htmlPath := automaticReportPath(*reportOutput, directory, *effort)
		if htmlPath == "" {
			panic("-report=none cannot be used with -report-only")
		}
		if !filepath.IsAbs(htmlPath) {
			htmlPath, err = filepath.Abs(htmlPath)
			if err != nil {
				panic(err)
			}
		}
		dataPath, err := writeComparisonArtifacts(directory, *effort, htmlPath)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stdout, "report: %s\ndata: %s\n", htmlPath, dataPath)
		return
	}

	absoluteBinary, err := filepath.Abs(*binary)
	if err != nil {
		panic(err)
	}
	absoluteNodeModules, err := filepath.Abs(*nodeModules)
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	otlp := newOTLPReceiver()
	defer otlp.Close()

	result := runOne(ctx, absoluteBinary, *agentName, *model, *effort, *scenario, absoluteNodeModules, strings.TrimRight(*gateway, "/"), otlp.URL())
	telemetryPath := *telemetryOutput
	if telemetryPath == "" && *output != "" {
		extension := filepath.Ext(*output)
		telemetryPath = strings.TrimSuffix(*output, extension) + ".otel.json"
	}
	if err := otlp.writeJSON(telemetryPath); err != nil {
		panic(err)
	}
	writer := io.Writer(os.Stdout)
	var outputFile *os.File
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			panic(err)
		}
		outputFile = file
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		panic(err)
	}
	if outputFile != nil {
		if err := outputFile.Close(); err != nil {
			panic(err)
		}
	}
	if *output != "" {
		directory := filepath.Dir(*output)
		htmlPath := automaticReportPath(*reportOutput, directory, *effort)
		if htmlPath != "" {
			if !filepath.IsAbs(htmlPath) {
				var err error
				htmlPath, err = filepath.Abs(htmlPath)
				if err != nil {
					panic(err)
				}
			}
			dataPath, err := writeComparisonArtifacts(directory, *effort, htmlPath)
			if err != nil {
				panic(err)
			}
			fmt.Fprintf(os.Stderr, "ACP comparison report: %s (data: %s)\n", htmlPath, dataPath)
		}
	}
	if result.Error != "" {
		os.Exit(1)
	}
}
