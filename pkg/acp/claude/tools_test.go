package claude

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestResultToTurn(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantStop acp.StopReason
		wantErr  bool
	}{
		{"success end_turn", `{"type":"result","subtype":"success","result":"done"}`, acp.StopReasonEndTurn, false},
		{"success max_tokens", `{"type":"result","subtype":"success","stop_reason":"max_tokens"}`, acp.StopReasonMaxTokens, false},
		{"success refusal", `{"type":"result","subtype":"success","stop_reason":"refusal"}`, acp.StopReasonRefusal, false},
		{"success is_error", `{"type":"result","subtype":"success","is_error":true,"result":"boom"}`, "", true},
		{"login required", `{"type":"result","subtype":"success","result":"Please run /login first"}`, "", true},
		{"error_during_execution", `{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["x","y"]}`, "", true},
		{"error_max_turns recoverable", `{"type":"result","subtype":"error_max_turns"}`, acp.StopReasonMaxTurnRequests, false},
		{"error_max_turns is_error", `{"type":"result","subtype":"error_max_turns","is_error":true,"errors":["limit"]}`, "", true},
		{"unknown subtype falls back to end_turn", `{"type":"result","subtype":"weird"}`, acp.StopReasonEndTurn, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := resultToTurn([]byte(tt.line))
			if tt.wantErr {
				if got.err == nil {
					t.Fatalf("expected error, got stop=%q", got.stop)
				}
				return
			}
			if got.err != nil {
				t.Fatalf("unexpected error: %v", got.err)
			}
			if got.stop != tt.wantStop {
				t.Errorf("stop = %q, want %q", got.stop, tt.wantStop)
			}
		})
	}
}

func TestResolveModelCanonicalizesContextHints(t *testing.T) {
	models := []ModelEntry{
		{ID: "default", Name: "Default", ResolvedModel: "claude-opus-4-8"},
		{ID: "opus", Name: "Opus", ResolvedModel: "claude-opus-4-8"},
		{ID: "opus[1m]", Name: "Opus [1m]", ResolvedModel: "claude-opus-4-8[1m]"},
	}
	if got := resolveModel(models, "opus-1m"); got == nil || got.ID != "opus[1m]" {
		t.Fatalf("opus-1m resolved to %#v", got)
	}
	if got := resolveModel(models, "claude-opus-4-8-1m"); got == nil || got.ID != "opus[1m]" {
		t.Fatalf("concrete 1m model resolved to %#v", got)
	}
	if got := resolveModel(models, "opus"); got == nil || got.ID != "opus" {
		t.Fatalf("bare opus resolved to %#v", got)
	}
	if got := resolveModel(models, "claude-opus-4-8"); got == nil || got.ID != "opus" {
		t.Fatalf("explicit concrete model resolved to %#v", got)
	}
	if got := resolveResumedModel(models, "claude-opus-4-8"); got == nil || got.ID != "default" {
		t.Fatalf("resumed default model resolved to %#v", got)
	}
}

func TestClaudeToolMetadataIncludesParent(t *testing.T) {
	u := toolCallStartUpdate("tool", "Bash", json.RawMessage(`{"command":"pwd"}`), "/tmp", acp.ToolCallStatusInProgress)
	withClaudeToolMeta(&u, "Bash", "parent-tool")
	claudeMeta, ok := u.ToolCall.Meta["claudeCode"].(map[string]any)
	if !ok || claudeMeta["toolName"] != "Bash" || claudeMeta["parentToolUseId"] != "parent-tool" {
		t.Fatalf("meta = %#v", u.ToolCall.Meta)
	}
}

func TestClaudeSystemIdleFailsOnlyActiveTurn(t *testing.T) {
	p := &claudeProc{results: make(chan turnResult, 1), subagentParents: map[string]string{}}
	p.beginTurn()
	p.handleSystem(context.Background(), nil, "s", cliEnvelope{Type: "system", Subtype: "session_state_changed", State: "idle"})
	r := <-p.results
	if r.err == nil {
		t.Fatal("expected idle-without-result error")
	}
	p.handleSystem(context.Background(), nil, "s", cliEnvelope{Type: "system", Subtype: "session_state_changed", State: "idle"})
	select {
	case extra := <-p.results:
		t.Fatalf("unexpected trailing-idle result: %#v", extra)
	default:
	}
}

func TestClaudeSystemTracksSubagentParents(t *testing.T) {
	p := &claudeProc{results: make(chan turnResult, 1), subagentParents: map[string]string{}}
	p.handleSystem(context.Background(), nil, "s", cliEnvelope{Type: "system", Subtype: "task_started", TaskID: "agent-1", ToolUseID: "parent-1"})
	if got := p.parentForAgent("agent-1"); got != "parent-1" {
		t.Fatalf("parent = %q", got)
	}
	p.handleSystem(context.Background(), nil, "s", cliEnvelope{Type: "system", Subtype: "task_updated", TaskID: "agent-1", Patch: struct {
		Status string `json:"status,omitempty"`
	}{Status: "completed"}})
	if got := p.parentForAgent("agent-1"); got != "" {
		t.Fatalf("parent after completion = %q", got)
	}
}

func TestResultLoginIsAuthRequired(t *testing.T) {
	got, _ := resultToTurn([]byte(`{"type":"result","subtype":"success","result":"Please run /login"}`))
	re, ok := got.err.(*acp.RequestError)
	if !ok {
		t.Fatalf("want *acp.RequestError, got %T", got.err)
	}
	if re.Code != -32000 {
		t.Errorf("want auth-required code -32000, got %d", re.Code)
	}
}

func TestToolInfoEditProducesDiff(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/proj/main.go","old_string":"foo","new_string":"bar"}`)
	info := toolInfoFromToolUse("Edit", input, "/proj")
	if info.kind != acp.ToolKindEdit {
		t.Errorf("kind = %q, want edit", info.kind)
	}
	if info.title != "Edit file" {
		t.Errorf("title = %q, want %q", info.title, "Edit file")
	}
	if len(info.content) != 1 || info.content[0].Diff == nil {
		t.Fatalf("expected one diff content block, got %+v", info.content)
	}
	d := info.content[0].Diff
	if d.Path != "/proj/main.go" || d.NewText != "bar" || d.OldText == nil || *d.OldText != "foo" {
		t.Errorf("diff = %+v", d)
	}
	if len(info.locations) != 1 || info.locations[0].Path != "/proj/main.go" {
		t.Errorf("locations = %+v", info.locations)
	}
	if info.rawInput != nil {
		t.Errorf("edit input duplicates its chip/diff: %#v", info.rawInput)
	}
}

func TestToolInfoMultiEditProducesDiffsWithoutRawEdits(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/proj/main.go","edits":[{"old_string":"a","new_string":"b"},{"old_string":"c","new_string":"d"}]}`)
	info := toolInfoFromToolUse("MultiEdit", input, "/proj")
	if info.title != "Edit file" || len(info.content) != 2 || info.rawInput != nil {
		t.Fatalf("info = %#v", info)
	}
	if info.content[0].Diff == nil || info.content[1].Diff == nil {
		t.Fatalf("diffs = %#v", info.content)
	}
}

func TestToolInfoSkillIncludesName(t *testing.T) {
	info := toolInfoFromToolUse("Skill", json.RawMessage(`{"skill":"commits","args":"--all"}`), "/proj")
	if info.title != "Load skill" {
		t.Errorf("title = %q, want %q", info.title, "Load skill")
	}
	if info.kind != acp.ToolKindOther {
		t.Errorf("kind = %q, want other", info.kind)
	}
	if len(info.content) != 0 {
		t.Errorf("content = %#v, want none", info.content)
	}
	input, ok := info.rawInput.(map[string]any)
	if !ok || input["skill"] != "commits" || input["args"] != "--all" {
		t.Errorf("display input = %#v", info.rawInput)
	}
}

func TestToolInfoReadTitleAndLocation(t *testing.T) {
	input := json.RawMessage(`{"file_path":"/proj/pkg/x.go","offset":10,"limit":5}`)
	info := toolInfoFromToolUse("Read", input, "/proj")
	if want := "Read file"; info.title != want {
		t.Errorf("title = %q, want %q", info.title, want)
	}
	if len(info.locations) != 1 || info.locations[0].Line == nil || *info.locations[0].Line != 10 {
		t.Errorf("locations = %+v", info.locations)
	}
	args, ok := info.rawInput.(map[string]any)
	if !ok || args["limit"] != float64(5) || args["offset"] != nil || args["file_path"] != nil {
		t.Errorf("display input = %#v", info.rawInput)
	}
}

func TestToolInfoGrepLabel(t *testing.T) {
	input := json.RawMessage(`{"pattern":"todo","path":"pkg","-i":true,"output_mode":"files_with_matches","glob":"*.go"}`)
	info := toolInfoFromToolUse("Grep", input, "/proj")
	if want := "Search files"; info.title != want {
		t.Errorf("title = %q, want %q", info.title, want)
	}
	if info.kind != acp.ToolKindSearch {
		t.Errorf("kind = %q, want search", info.kind)
	}
	if len(info.locations) != 1 || info.locations[0].Path != "/proj/pkg" {
		t.Errorf("locations = %#v", info.locations)
	}
	args, ok := info.rawInput.(map[string]any)
	if !ok || args["pattern"] != "todo" || args["path"] != nil {
		t.Errorf("display input = %#v", info.rawInput)
	}
}

func TestToolCallStartUsesLocationWithoutSyntheticPathInput(t *testing.T) {
	update := toolCallStartUpdate(
		"read-1", "Read", json.RawMessage(`{"file_path":"/proj/main.go"}`), "/proj",
		acp.ToolCallStatusInProgress,
	)
	if update.ToolCall == nil {
		t.Fatal("missing tool call")
	}
	if update.ToolCall.Title != "Read file" || len(update.ToolCall.Locations) != 1 {
		t.Fatalf("tool call = %#v", update.ToolCall)
	}
	if update.ToolCall.RawInput != nil {
		t.Fatalf("location was mirrored into input: %#v", update.ToolCall.RawInput)
	}
}

func TestUnknownToolUsesRawInputOnlyOnce(t *testing.T) {
	info := toolInfoFromToolUse("mcp__example", json.RawMessage(`{"query":"value"}`), "/proj")
	if info.rawInput == nil {
		t.Fatal("missing raw input")
	}
	if len(info.content) != 0 {
		t.Fatalf("raw input was duplicated as content: %#v", info.content)
	}
}

func TestPlanEntriesFromTodoWrite(t *testing.T) {
	input := json.RawMessage(`{"todos":[{"content":"a","status":"completed"},{"content":"b","status":"in_progress"},{"content":"c","status":"pending"}]}`)
	entries, ok := planEntriesFromTodoWrite(input)
	if !ok || len(entries) != 3 {
		t.Fatalf("entries=%+v ok=%v", entries, ok)
	}
	if entries[0].Status != acp.PlanEntryStatusCompleted ||
		entries[1].Status != acp.PlanEntryStatusInProgress ||
		entries[2].Status != acp.PlanEntryStatusPending {
		t.Errorf("statuses = %+v", entries)
	}

	if _, ok := planEntriesFromTodoWrite(json.RawMessage(`{}`)); ok {
		t.Errorf("expected ok=false for missing todos")
	}
}

func TestMarkdownEscapeLengthensFence(t *testing.T) {

	got := markdownEscape("````\ncode\n````")
	if want := "`````\n````\ncode\n````\n`````"; got != want {
		t.Errorf("markdownEscape = %q, want %q", got, want)
	}
}

func TestResultUsageAndUpdate(t *testing.T) {
	line := `{"type":"result","subtype":"success","total_cost_usd":0.05,` +
		`"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":300,"cache_creation_input_tokens":40},` +
		`"modelUsage":{"claude-haiku":{"contextWindow":200000},"claude-opus":{"contextWindow":1000000}}}`
	tr, upd := resultToTurn([]byte(line))
	if tr.usage == nil {
		t.Fatal("expected usage")
	}
	if tr.usage.TotalTokens != 460 || tr.usage.InputTokens != 100 || tr.usage.OutputTokens != 20 {
		t.Errorf("usage = %+v", tr.usage)
	}
	if tr.usage.CachedReadTokens == nil || *tr.usage.CachedReadTokens != 300 {
		t.Errorf("cachedRead = %v", tr.usage.CachedReadTokens)
	}
	if upd == nil || upd.UsageUpdate == nil {
		t.Fatal("expected usage_update")
	}
	if upd.UsageUpdate.Used != 460 || upd.UsageUpdate.Size != 1000000 {
		t.Errorf("usage_update used=%d size=%d", upd.UsageUpdate.Used, upd.UsageUpdate.Size)
	}
	if upd.UsageUpdate.Cost == nil || upd.UsageUpdate.Cost.Amount != 0.05 {
		t.Errorf("cost = %+v", upd.UsageUpdate.Cost)
	}
}

func TestMCPConfigJSON(t *testing.T) {
	if got := mcpConfigJSON(nil); got != "" {
		t.Errorf("empty servers should yield empty string, got %q", got)
	}
	servers := []acp.McpServer{
		{Stdio: &acp.McpServerStdio{Name: "fs", Command: "srv", Args: []string{"-x"}, Env: []acp.EnvVariable{{Name: "K", Value: "V"}}}},
		{Http: &acp.McpServerHttpInline{Name: "web", Url: "https://x", Headers: []acp.HttpHeader{{Name: "A", Value: "B"}}}},
	}
	got := mcpConfigJSON(servers)
	var parsed struct {
		McpServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid json: %v (%s)", err, got)
	}
	if parsed.McpServers["fs"]["command"] != "srv" || parsed.McpServers["fs"]["type"] != "stdio" {
		t.Errorf("fs config = %+v", parsed.McpServers["fs"])
	}
	if parsed.McpServers["web"]["url"] != "https://x" || parsed.McpServers["web"]["type"] != "http" {
		t.Errorf("web config = %+v", parsed.McpServers["web"])
	}
}

func TestToolCallTrackerEmitsStartOnceThenRefines(t *testing.T) {
	tracker := newToolCallTracker()

	var calls []string
	start := func() error { calls = append(calls, "start"); return nil }
	refine := func() error { calls = append(calls, "refine"); return nil }

	if err := tracker.emit("tool-1", start, refine); err != nil {
		t.Fatal(err)
	}
	if err := tracker.emit("tool-1", start, refine); err != nil {
		t.Fatal(err)
	}
	if err := tracker.emit("tool-2", start, refine); err != nil {
		t.Fatal(err)
	}

	want := []string{"start", "refine", "start"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, calls[i], want[i])
		}
	}
}

func TestShouldEmitToolCall(t *testing.T) {
	cases := map[string]bool{
		"Bash":      true,
		"Write":     true,
		"TodoWrite": false,
		"Task":      false,
		"Agent":     false,
	}
	for name, want := range cases {
		if got := shouldEmitToolCall(name); got != want {
			t.Errorf("shouldEmitToolCall(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBashImageResultBlocksSurfacesImage(t *testing.T) {
	raw := []byte(`[{"type":"text","text":"saved"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]`)
	blocks, ok := bashImageResultBlocks(raw)
	if !ok {
		t.Fatal("expected mixed content to be detected")
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d: %+v", len(blocks), blocks)
	}
	if blocks[0].Content == nil || blocks[0].Content.Content.Text == nil || blocks[0].Content.Content.Text.Text != "saved" {
		t.Errorf("text block = %+v", blocks[0])
	}
	if blocks[1].Content == nil || blocks[1].Content.Content.Image == nil || blocks[1].Content.Content.Image.Data != "AAAA" {
		t.Errorf("image block = %+v", blocks[1])
	}

	if _, ok := bashImageResultBlocks([]byte(`[{"type":"text","text":"plain output"}]`)); ok {
		t.Error("text-only array should fall back to normal extraction (ok=false)")
	}

	if blocks, ok := bashImageResultBlocks([]byte(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}}]`)); ok {
		t.Errorf("non-text content with no extractable data should fall back (ok=false), got blocks=%+v", blocks)
	}
}

func TestResolveModelAlias(t *testing.T) {
	models := []ModelEntry{{ID: "claude-opus-4-8", Name: "Opus"}, {ID: "claude-haiku-4-5", Name: "Haiku"}}
	if m := resolveModel(models, "claude-opus-4-8"); m == nil || m.ID != "claude-opus-4-8" {
		t.Errorf("exact id failed: %v", m)
	}
	if m := resolveModel(models, "opus"); m == nil || m.ID != "claude-opus-4-8" {
		t.Errorf("alias 'opus' failed: %v", m)
	}
	if m := resolveModel(models, "Haiku"); m == nil || m.ID != "claude-haiku-4-5" {
		t.Errorf("name match failed: %v", m)
	}
	if m := resolveModel(models, "gpt-5"); m != nil {
		t.Errorf("unrelated should be nil, got %v", m)
	}
}

func TestToolKindForMatchesToolInfo(t *testing.T) {
	for _, name := range []string{
		"Read", "Glob", "Grep", "WebFetch", "WebSearch",
		"Edit", "Write", "MultiEdit", "Bash", "Agent", "Task", "ExitPlanMode",
	} {
		info := toolInfoFromToolUse(name, json.RawMessage(`{}`), "/tmp")
		if got := toolKindFor(name); got != info.kind {
			t.Errorf("toolKindFor(%q) = %q, toolInfoFromToolUse kind = %q", name, got, info.kind)
		}
	}
}

func TestAlwaysAllowPermissions(t *testing.T) {
	suggested := controlRequestBody{
		ToolName:              "Bash",
		PermissionSuggestions: json.RawMessage(`[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"npm test:*"}],"behavior":"allow","destination":"session"}]`),
	}
	perms, ok := alwaysAllowPermissions(suggested).([]any)
	if !ok || len(perms) != 1 {
		t.Fatalf("suggestions passthrough = %#v", perms)
	}
	rule, _ := perms[0].(map[string]any)
	if rule["type"] != "addRules" {
		t.Errorf("suggestion rule = %#v", rule)
	}

	fallback, ok := alwaysAllowPermissions(controlRequestBody{ToolName: "WebFetch"}).([]any)
	if !ok || len(fallback) != 1 {
		t.Fatalf("fallback = %#v", fallback)
	}
	rule, _ = fallback[0].(map[string]any)
	if rule["behavior"] != "allow" || rule["destination"] != "session" {
		t.Errorf("fallback rule = %#v", rule)
	}

	if got := alwaysAllowPermissions(controlRequestBody{}); got != nil {
		t.Errorf("no tool name should yield nil, got %#v", got)
	}
}

func TestTaskPlan(t *testing.T) {
	p := newTaskPlan()

	p.noteCreate("tu1", json.RawMessage(`{"subject":"first","description":"d","activeForm":"Doing first"}`))
	if p.completeCreate("tu1", "Task #1 created successfully: first", false) != true {
		t.Fatal("create #1 should register")
	}
	p.noteCreate("tu2", json.RawMessage(`{"subject":"second"}`))
	if !p.completeCreate("tu2", "Task #2 created successfully: second", false) {
		t.Fatal("create #2 should register")
	}

	entries := p.entries()
	if len(entries) != 2 || entries[0].Content != "first" || entries[0].Status != acp.PlanEntryStatusPending {
		t.Fatalf("entries after create = %#v", entries)
	}

	p.noteUpdate("update-1", json.RawMessage(`{"taskId":"1","status":"completed"}`))
	if !p.completeUpdate("update-1", `{"success":true,"taskId":"1"}`, false) {
		t.Fatal("update #1 should change state")
	}
	p.noteUpdate("update-2", json.RawMessage(`{"taskId":"1","status":"completed"}`))
	if p.completeUpdate("update-2", `{"success":true,"taskId":"1"}`, false) {
		t.Fatal("repeat update should be a no-op")
	}
	p.noteUpdate("update-3", json.RawMessage(`{"taskId":"99","status":"completed"}`))
	if p.completeUpdate("update-3", "ok", false) {
		t.Fatal("unknown task id should be a no-op")
	}
	if entries := p.entries(); entries[0].Status != acp.PlanEntryStatusCompleted || entries[1].Status != acp.PlanEntryStatusPending {
		t.Fatalf("entries after update = %#v", entries)
	}

	if p.completeCreate("tu-unknown", "Task #3 created successfully: x", false) {
		t.Fatal("result without matching create should be a no-op")
	}
	p.noteCreate("tu3", json.RawMessage(`{"subject":"third"}`))
	if p.completeCreate("tu3", "something went wrong", false) {
		t.Fatal("unparseable result should not register")
	}

	p.noteUpdate("failed-update", json.RawMessage(`{"taskId":"2","status":"completed"}`))
	if p.completeUpdate("failed-update", "Task #2 not found", false) {
		t.Fatal("logically failed update should not change state")
	}
	if entries := p.entries(); entries[1].Status != acp.PlanEntryStatusPending {
		t.Fatalf("failed update changed task state: %#v", entries)
	}

	if entries, ok := p.unfinishedEntries(); !ok || len(entries) != 2 {
		t.Fatalf("unfinished entries = %#v, %v", entries, ok)
	}

	p.noteUpdate("delete-2", json.RawMessage(`{"taskId":"2","status":"deleted"}`))
	if !p.completeUpdate("delete-2", `{"success":true,"taskId":"2"}`, false) {
		t.Fatal("delete #2 should change state")
	}
	if entries := p.entries(); len(entries) != 1 || entries[0].Content != "first" {
		t.Fatalf("entries after delete = %#v", entries)
	}
	p.clear()
	if entries, ok := p.unfinishedEntries(); ok || len(entries) != 0 {
		t.Fatalf("entries after clear = %#v, %v", entries, ok)
	}
	if !p.applyTaskList(`{"tasks":[{"id":"7","subject":"restored","status":"in_progress"}]}`, false) {
		t.Fatal("structured task list should restore state")
	}
	if entries := p.entries(); len(entries) != 1 || entries[0].Content != "restored" || entries[0].Status != acp.PlanEntryStatusInProgress {
		t.Fatalf("entries after task list = %#v", entries)
	}
	if !p.applyTaskList("No tasks found", false) || len(p.entries()) != 0 {
		t.Fatalf("empty task list did not clear state: %#v", p.entries())
	}
}

func TestParseAskQuestions(t *testing.T) {
	qs := parseAskQuestions(json.RawMessage(`{"questions":[
		{"question":"Which color?","header":"Color","options":[{"label":"Red","description":"warm"},{"label":"Blue"}]},
		{"question":"Pick letters","multiSelect":true,"options":["a","b"]},
		{"question":"","options":["x"]},
		{"question":"no options","options":[]}
	]}`))
	if len(qs) != 2 {
		t.Fatalf("questions = %#v", qs)
	}
	if qs[0].Question != "Which color?" || len(qs[0].Options) != 2 || qs[0].Options[0].Label != "Red" || qs[0].Options[0].Description != "warm" {
		t.Errorf("object options = %#v", qs[0])
	}
	if !qs[1].MultiSelect || len(qs[1].Options) != 2 || qs[1].Options[1].Label != "b" {
		t.Errorf("string options = %#v", qs[1])
	}
	if got := parseAskQuestions(json.RawMessage(`{}`)); len(got) != 0 {
		t.Errorf("empty input = %#v", got)
	}
}

func TestAskElicitationSchema(t *testing.T) {
	single := askElicitationSchema([]askQuestion{{
		Question: "Which color?", Header: "Color",
		Options: []askOption{{Label: "Red", Description: "warm"}, {Label: "Blue"}},
	}})
	field, _ := single.Properties["question_0"].(map[string]any)
	if field["type"] != "string" || field["title"] != "Color" || field["description"] != nil {
		t.Errorf("single field = %#v", field)
	}
	oneOf, _ := field["oneOf"].([]map[string]any)
	if len(oneOf) != 2 || oneOf[0]["const"] != "Red" || oneOf[0]["description"] != "warm" {
		t.Errorf("oneOf = %#v", oneOf)
	}
	if custom, _ := single.Properties["question_0_custom"].(map[string]any); custom["type"] != "string" {
		t.Errorf("custom field = %#v", custom)
	} else {
		meta, _ := custom["_meta"].(map[string]any)
		marker, _ := meta["_askUserQuestionCustomAnswer"].(map[string]any)
		if marker["questionId"] != "question_0" || marker["isCustomAnswer"] != true {
			t.Errorf("custom field marker = %#v", marker)
		}
	}

	multi := askElicitationSchema([]askQuestion{
		{Question: "Q1", MultiSelect: true, Options: []askOption{{Label: "a"}}},
		{Question: "Q2", Options: []askOption{{Label: "x"}}},
	})
	f0, _ := multi.Properties["question_0"].(map[string]any)
	if f0["type"] != "array" || f0["description"] != "Q1" {
		t.Errorf("multiSelect field = %#v", f0)
	}
}

func TestAskAnswersFromContent(t *testing.T) {
	questions := []askQuestion{
		{Question: "Q1", Options: []askOption{{Label: "a"}}},
		{Question: "Q2", MultiSelect: true, Options: []askOption{{Label: "x"}, {Label: "y"}}},
		{Question: "Q3", Options: []askOption{{Label: "z"}}},
	}
	answers := askAnswersFromContent(questions, map[string]any{
		"question_0":        "a",
		"question_1":        []any{"x", "y"},
		"question_2":        "z",
		"question_2_custom": " my own answer ",
	})
	if answers["Q1"] != "a" || answers["Q2"] != "x, y" || answers["Q3"] != "my own answer" {
		t.Errorf("answers = %#v", answers)
	}
	if got := askAnswersFromContent(questions, map[string]any{}); len(got) != 0 {
		t.Errorf("empty content = %#v", got)
	}
}
