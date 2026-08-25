package code

import (
	"fmt"
	"slices"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/markdown"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

// cellFlow carries the spacing state between chat cells and applies the
// rules in one place: one-line tool cells stack tight, a blank line separates
// a cell from its neighbor as soon as either side is multi-line, thought
// cells stay tight only between consecutive one-line thoughts, and text
// always sits apart from tool output. The committed chat and the live
// streaming tail share it, so spacing does not change when a cell finalizes.
type cellFlow struct {
	tool         bool // previous cell was a tool or thought
	multiline    bool
	thought      bool // previous cell was a thought
	thoughtMulti bool
}

// beforeTool reports whether a blank line belongs before a tool cell and
// advances the state.
func (f *cellFlow) beforeTool(multiline bool) bool {
	gap := f.tool && (f.multiline || multiline)
	*f = cellFlow{tool: true, multiline: multiline}
	return gap
}

func (f *cellFlow) beforeThought(multiline bool) bool {
	gap := f.tool && (!f.thought || f.thoughtMulti)
	*f = cellFlow{tool: true, multiline: true, thought: true, thoughtMulti: multiline}
	return gap
}

// gap ends a run of tool and thought cells: it reports whether the run is
// still owed its trailing blank line and resets the state. Text cells,
// notices, and separators call it before rendering.
func (f *cellFlow) gap() bool {
	owed := f.tool
	*f = cellFlow{}
	return owed
}

// cellUser renders the user's prompt echo: a `›` prefix on a subtle
// background band, text starting at the shared 2-column gutter.
func cellUser(text string, width int) []string {
	t := theme.Default
	band := ansi.Bg(t.Selection)

	bandWidth := max(width-len(cellIndent), 14)
	inner := bandWidth - 3

	var lines []string
	first := true

	for line := range strings.SplitSeq(strings.TrimRight(markdown.Sanitize(text), "\n"), "\n") {
		for _, wl := range ansi.Wrap(line, inner) {
			prefix := "  "
			if first {
				prefix = "› "
				first = false
			}
			pad := max(inner-ansi.Width(wl), 0)
			lines = append(lines, cellIndent+band+fg(t.BrBlack)+prefix+fg(t.Foreground)+wl+strings.Repeat(" ", pad+1)+ansi.Reset)
		}
	}

	lines = append(lines, "")
	return lines
}

// isCommandEcho reports user text that echoes a slash invocation; the full
// prompt behind it travels as hidden content.
func isCommandEcho(text string) bool {
	return strings.HasPrefix(text, "/") && !strings.Contains(text, "\n")
}

// cellCommand renders a slash invocation echo as a single accent line instead
// of a full user prompt band.
func cellCommand(text string, width int) []string {
	t := theme.Default

	name, args, _ := strings.Cut(markdown.Sanitize(text), " ")
	line := colored(t.Cyan, "› "+name)
	if args != "" {
		line += " " + dim(args)
	}

	return []string{cellIndent + ansi.Truncate(line, width-len(cellIndent), "…") + ansi.Reset, ""}
}

// cellAssistant renders assistant markdown behind a status circle: dim while
// streaming, green when committed, red on failure.
func cellAssistant(text string, width int, circle ansi.Color) []string {
	inner := max(width-len(cellIndent)-2, 10)

	var lines []string
	first := true

	for line := range strings.SplitSeq(strings.TrimRight(markdown.Render(text), "\n"), "\n") {
		for _, wl := range ansi.Wrap(line, inner) {
			prefix := "  "
			if first {
				prefix = colored(circle, "● ")
				first = false
			}
			lines = append(lines, cellIndent+prefix+wl)
		}
	}

	lines = append(lines, "")
	return lines
}

func cellReasoning(summary string, width int, full bool) []string {
	t := theme.Default
	style := fg(t.BrBlack) + ansi.Italic

	if !full {
		tail := lastNonEmptyLine(markdown.Sanitize(summary))
		line := style + "• " + tail
		return []string{cellIndent + ansi.Truncate(line, width-len(cellIndent), "…") + ansi.Reset}
	}

	inner := max(width-len(cellIndent)-2, 10)

	// Markdown accents survive, but every reset falls back to the dim italic
	// base so the whole thought keeps its muted look.
	rendered := strings.ReplaceAll(markdown.Render(summary), ansi.Reset, ansi.Reset+style)

	var lines []string
	first := true

	for line := range strings.SplitSeq(strings.TrimRight(rendered, "\n"), "\n") {
		for _, wl := range ansi.Wrap(style+line, inner) {
			prefix := "  "
			if first {
				prefix = style + "• "
				first = false
			}
			lines = append(lines, cellIndent+prefix+wl+ansi.Reset)
		}
	}

	return lines
}

// cellReasoningHeadings renders the structured reasoning headings retained in
// the normal chat. Complete reasoning bodies are reserved for the transcript.
func cellReasoningHeadings(headings string, width int) []string {
	var lines []string
	for heading := range strings.SplitSeq(headings, "\n") {
		if heading != "" {
			lines = append(lines, cellReasoning(heading, width, false)...)
		}
	}
	return lines
}

// extractReasoningHeader returns the first complete structured heading in a
// reasoning part. Incomplete delimiters are left buffered until a later delta.
func extractReasoningHeader(summary string) string {
	header, _, _ := strings.Cut(extractReasoningHeadings(summary), "\n")
	return header
}

// extractReasoningHeadings recovers every structured bold heading from a
// committed summary whose original provider part boundaries are no longer
// represented separately.
func extractReasoningHeadings(summary string) string {
	var headings []string
	for offset := 0; offset < len(summary); {
		relativeStart := strings.Index(summary[offset:], "**")
		if relativeStart < 0 {
			break
		}
		start := offset + relativeStart
		rest := summary[start+2:]
		end := strings.Index(rest, "**")
		if end < 0 {
			break
		}
		lineStart := strings.LastIndex(summary[:start], "\n") + 1
		if strings.TrimSpace(summary[lineStart:start]) == "" {
			if heading := normalizeReasoningHeading(rest[:end]); heading != "" {
				headings = append(headings, heading)
			}
		}
		offset = start + 2 + end + 2
	}
	return strings.Join(headings, "\n")
}

func normalizeReasoningHeading(heading string) string {
	return strings.Join(strings.Fields(markdown.Sanitize(heading)), " ")
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for _, line := range slices.Backward(lines) {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

func isShellTool(name string) bool {
	return name == "shell" || name == "exec_command" || name == "exec_session"
}

// toolLabel maps internal tool names to friendlier display titles.
func toolLabel(name string) string {
	switch name {
	case "task_send":
		return "agent follow-up"
	case "task_stop":
		return "agent stop"
	}
	return name
}

// isTaskCommand reports task tools whose success result is model-directed
// boilerplate the chat should not echo.
func isTaskCommand(name string) bool {
	return name == "task_send" || name == "task_stop"
}

func isMutationTool(name string) bool {
	return name == "edit" || name == "write"
}

// toolTitleLine renders the header line for a tool call. info carries a
// one-line result summary appended dimly after the hint; errored switches the
// marker to red.
func toolTitleLine(name, hint, info string, width int, running, errored bool) string {
	t := theme.Default
	hint = markdown.Sanitize(strings.ReplaceAll(hint, "\n", " "))

	marker := t.BrBlack
	if errored {
		marker = t.Red
	}

	var line string
	switch {
	case isShellTool(name):
		prompt := t.Magenta
		if errored {
			prompt = t.Red
		}
		line = colored(prompt, "$ ") + bold(hint)
	case name == "agent":
		line = colored(marker, "• ") + bold("agent") + " " + dim(hint)
	default:
		line = colored(marker, "• ") + bold(toolLabel(name))
		if hint != "" {
			line += " " + dim(hint)
		}
	}

	if info != "" {
		line += dim(" · " + markdown.Sanitize(strings.ReplaceAll(info, "\n", " ")))
	}

	if running {
		line += dim(" …")
	}

	return cellIndent + ansi.Truncate(line, width-len(cellIndent), "…") + ansi.Reset
}

// tailPreview keeps the last n lines — command verdicts live at the end.
func tailPreview(lines []string, n int) []string {
	if len(lines) <= n+1 {
		return lines
	}
	out := []string{fmt.Sprintf("… %d earlier lines (ctrl+o transcript)", len(lines)-n)}
	return append(out, lines[len(lines)-n:]...)
}

// headPreview keeps the first n lines — diffs read from the top.
func headPreview(lines []string, n int) []string {
	if len(lines) <= n+1 {
		return lines
	}
	out := append([]string(nil), lines[:n]...)
	return append(out, fmt.Sprintf("… +%d more (ctrl+o transcript)", len(lines)-n))
}

func cellTool(result *agent.ToolResult, width int, full bool) []string {
	name := result.Name

	if name == "todo" {
		return cellTodo(result.Args, width)
	}

	hint := tool.ExtractHint(result.Args, result.Name)
	output := strings.TrimRight(result.Content, "\n")
	errored := result.IsError || strings.HasPrefix(output, "error:")

	colorize := func(s string) string { return dim(markdown.Sanitize(s)) }
	preview := strings.Split(output, "\n")

	info := ""
	body := preview

	switch {
	case isShellTool(name):
		if !full {
			body = tailPreview(preview, 3)
		}
	case isMutationTool(name):
		t := theme.Default
		colorize = func(s string) string {
			switch {
			case strings.HasPrefix(s, "+"):
				return colored(t.Green, markdown.Sanitize(s))
			case strings.HasPrefix(s, "-"):
				return colored(t.Red, markdown.Sanitize(s))
			}
			return dim(markdown.Sanitize(s))
		}
		if !full {
			body = headPreview(preview, 5)
		}
	default:
		if !full {
			body = nil
			if isTaskCommand(name) && !errored {
				break
			}
			if len(preview) == 1 {
				info = preview[0]
			} else if output != "" {
				info = fmt.Sprintf("%d lines", len(preview))
			}
		}
	}

	lines := []string{toolTitleLine(name, hint, info, width, false, errored)}
	if output == "" || len(body) == 0 {
		return lines
	}

	lines = append(lines, continuationWrap(strings.Join(body, "\n"), width, colorize)...)

	return lines
}

func cellToolProgress(name, hint, progress string, width int) []string {
	lines := []string{toolTitleLine(name, hint, "", width, true, false)}

	if progress != "" {
		inner := max(width-len(cellIndent)-2, 10)
		text := markdown.Sanitize(strings.ReplaceAll(progress, "\n", " "))
		lines = append(lines, cellIndent+"  "+dim(ansi.Truncate(text, inner, "…")))
	}

	return lines
}

func cellTodo(argsJSON string, width int) []string {
	items := tool.ParseTodoItems(argsJSON)
	if len(items) == 0 {
		return []string{toolTitleLine("todo", "", "", width, false, false)}
	}
	return cellTodoItems(items, width)
}

func cellTodoItems(items []tool.TodoItem, width int) []string {
	t := theme.Default

	completed := 0
	for _, item := range items {
		if item.Status == "completed" {
			completed++
		}
	}

	lines := []string{cellIndent + dim("• ") + bold("plan") + " " + dim(fmt.Sprintf("%d/%d", completed, len(items)))}

	inner := max(width-len(cellIndent)-4, 10)

	for _, item := range items {
		var line string
		content := markdown.Sanitize(item.Content)
		switch item.Status {
		case "completed":
			line = fg(t.Green) + "✔ " + ansi.Reset + fg(t.BrBlack) + ansi.Strike + content
		case "in_progress":
			line = fg(t.Cyan) + ansi.Bold + "□ " + content
		default:
			line = fg(t.BrBlack) + "□ " + content
		}
		for i, wl := range ansi.Wrap(line, inner) {
			prefix := cellIndent + "  "
			if i > 0 {
				prefix += "  "
			}
			lines = append(lines, prefix+wl+ansi.Reset)
		}
	}

	return lines
}

func cellNotice(message string, color ansi.Color, width int) []string {
	lines := indentWrap(colored(color, markdown.Sanitize(message)), width)
	lines = append(lines, "")
	return lines
}

func cellError(title, message string, width int) []string {
	t := theme.Default

	inner := max(width-len(cellIndent)-2, 10)

	var lines []string
	first := true

	for _, wl := range ansi.Wrap(fg(t.Red)+ansi.Bold+markdown.Sanitize(title), inner) {
		prefix := "  "
		if first {
			prefix = colored(t.Red, "● ")
			first = false
		}
		lines = append(lines, cellIndent+prefix+wl+ansi.Reset)
	}

	for line := range strings.SplitSeq(strings.TrimRight(message, "\n"), "\n") {
		if line == "" {
			continue
		}
		for _, wl := range ansi.Wrap(dim(markdown.Sanitize(line)), inner) {
			lines = append(lines, cellIndent+"  "+wl)
		}
	}

	lines = append(lines, "")
	return lines
}

// cellPrompt renders a confirmation or elicitation request with a `▌` accent
// bar.
func cellPrompt(title, message, hint string, width int) []string {
	t := theme.Default
	bar := fg(t.Yellow) + "▌ " + ansi.Reset

	inner := max(width-len(cellIndent)-2, 10)

	var lines []string

	if title != "" {
		for _, wl := range ansi.Wrap(ansi.Bold+markdown.Sanitize(title), inner) {
			lines = append(lines, cellIndent+bar+wl+ansi.Reset)
		}
		lines = append(lines, cellIndent+strings.TrimRight(bar, " "))
	}

	for line := range strings.SplitSeq(strings.TrimRight(message, "\n"), "\n") {
		for _, wl := range ansi.Wrap(markdown.Sanitize(line), inner) {
			lines = append(lines, cellIndent+bar+wl+ansi.Reset)
		}
	}

	if hint != "" {
		lines = append(lines, cellIndent+bar+hint+ansi.Reset)
	}

	lines = append(lines, "")
	return lines
}

// cellTurnSeparator emits the between-turns rule with a work summary.
func cellTurnSeparator(elapsed string, tools, thoughts int, width int) []string {
	var parts []string
	if elapsed != "" {
		parts = append(parts, "worked for "+elapsed)
	}
	if tools > 0 {
		unit := "tools"
		if tools == 1 {
			unit = "tool"
		}
		parts = append(parts, fmt.Sprintf("%d %s", tools, unit))
	}
	if thoughts > 0 {
		unit := "thoughts"
		if thoughts == 1 {
			unit = "thought"
		}
		parts = append(parts, fmt.Sprintf("%d %s", thoughts, unit))
	}

	label := strings.Join(parts, " · ")

	inner := max(width-2*len(cellIndent), 10)

	var line string
	if label == "" || ansi.Width(label)+8 > inner {
		line = strings.Repeat("─", inner)
	} else {
		rest := inner - ansi.Width(label) - 5
		line = "── " + label + " " + strings.Repeat("─", rest)
	}

	return []string{cellIndent + dim(line), ""}
}
