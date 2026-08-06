package code

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type transcriptKind int

const (
	transcriptSeparator transcriptKind = iota
	transcriptUser
	transcriptAssistant
	transcriptTool
	transcriptReasoning
	transcriptNotice
	transcriptLiveTool
)

type transcriptEntry struct {
	key        string
	kind       transcriptKind
	raw        string
	selectable bool
	render     func(width int, expanded bool) []string
}

type transcriptCache struct {
	raw      string
	width    int
	expanded bool
	theme    string
	selected bool
	query    string
	lines    []string
}

type transcriptOverlay struct {
	app *App

	entries    []transcriptEntry
	selected   int
	offset     int
	height     int
	follow     bool
	lineOffset int
	lineMax    int

	expanded map[string]bool
	cache    map[string]transcriptCache

	query     string
	searching bool
	matches   []int
	matchPos  int
	notice    string
}

func (a *App) showTranscript() {
	a.openOverlay(&transcriptOverlay{
		app:      a,
		selected: -1,
		follow:   true,
		expanded: map[string]bool{},
		cache:    map[string]transcriptCache{},
	})
}

func (o *transcriptOverlay) buildEntries() {
	selectedKey := ""
	if o.selected >= 0 && o.selected < len(o.entries) {
		selectedKey = o.entries[o.selected].key
	}

	var entries []transcriptEntry
	annotations := func(index int) {
		for i, annotation := range o.app.annotations {
			if annotation.afterMessages != index {
				continue
			}
			ann := annotation
			key := fmt.Sprintf("annotation:%d:%d", index, i)
			entries = append(entries, transcriptEntry{
				key: key, kind: transcriptNotice, selectable: true,
				raw: "session notice",
				render: func(width int, _ bool) []string {
					return ann.render(width)
				},
			})
		}
	}

	turn := 0
	messages := o.app.agent.Messages(o.app.sessionID)
	for mi, message := range messages {
		annotations(mi)
		if message.Hidden {
			continue
		}
		if message.Role == agent.RoleUser {
			if turn > 0 {
				entries = append(entries, transcriptEntry{
					key: fmt.Sprintf("separator:%d", turn), kind: transcriptSeparator,
					render: func(width int, _ bool) []string { return cellTurnSeparator("", 0, 0, width) },
				})
			}
			turn++
		}

		for ci, content := range message.Content {
			if content.Hidden {
				continue
			}
			baseKey := fmt.Sprintf("message:%d:%d", mi, ci)
			switch {
			case content.ToolResult != nil:
				result := content.ToolResult
				if o.app.isToolHidden(result.Name) {
					continue
				}
				key := baseKey + ":tool:" + result.ID
				raw := result.Name + " " + tool.ExtractHint(result.Args, result.Name) + "\n" + result.Content
				entries = append(entries, transcriptEntry{
					key: key, kind: transcriptTool, raw: raw, selectable: true,
					render: func(width int, expanded bool) []string { return cellTool(result, width, expanded) },
				})
			case content.Reasoning != nil && content.Reasoning.Summary != "":
				reasoning := content.Reasoning
				key := baseKey + ":reasoning:" + reasoning.ID
				entries = append(entries, transcriptEntry{
					key: key, kind: transcriptReasoning, raw: reasoning.Summary, selectable: true,
					render: func(width int, expanded bool) []string {
						return cellReasoning(reasoning.Summary, width, expanded)
					},
				})
			case strings.TrimSpace(content.Text) != "":
				text := content.Text
				if message.Role == agent.RoleUser {
					entries = append(entries, transcriptEntry{
						key: baseKey + ":user", kind: transcriptUser, raw: text, selectable: true,
						render: func(width int, _ bool) []string {
							if isCommandEcho(text) {
								return cellCommand(text, width)
							}
							return cellUser(text, width)
						},
					})
				} else if message.Role == agent.RoleAssistant {
					entries = append(entries, transcriptEntry{
						key: baseKey + ":assistant", kind: transcriptAssistant, raw: text, selectable: true,
						render: func(width int, _ bool) []string {
							return cellAssistant(text, width, theme.Default.Green)
						},
					})
				}
			}
		}
	}
	annotations(len(messages))

	// Live cells mirror every in-flight snapshot in the chat tail.
	for i, snapshot := range o.app.snapshotStreamState() {
		prefix := fmt.Sprintf("live:%d", i)
		if snapshot.userText != "" {
			text := snapshot.userText
			entries = append(entries, transcriptEntry{
				key: prefix + ":user", kind: transcriptUser, raw: text, selectable: true,
				render: func(width int, _ bool) []string {
					if isCommandEcho(text) {
						return cellCommand(text, width)
					}
					return cellUser(text, width)
				},
			})
		}
		if snapshot.toolName != "" && !o.app.isToolHidden(snapshot.toolName) {
			live := snapshot
			kind := transcriptLiveTool
			if snapshot.toolResult != nil {
				kind = transcriptTool
			}
			entries = append(entries, transcriptEntry{
				key: prefix + ":tool", kind: kind, selectable: true, raw: snapshot.toolText(),
				render: func(width int, expanded bool) []string {
					return live.toolLines(width, expanded)
				},
			})
		}
		if snapshot.text != "" {
			streamingText := snapshot.text
			entries = append(entries, transcriptEntry{
				key: prefix + ":text", kind: transcriptAssistant, raw: streamingText, selectable: true,
				render: func(width int, _ bool) []string {
					return cellAssistant(streamingText, width, theme.Default.BrBlack)
				},
			})
		}
		if snapshot.reasoning != "" {
			streamingReasoning := snapshot.reasoning
			entries = append(entries, transcriptEntry{
				key: prefix + ":reasoning", kind: transcriptReasoning, raw: streamingReasoning, selectable: true,
				render: func(width int, expanded bool) []string {
					return cellReasoning(streamingReasoning, width, expanded)
				},
			})
		}
	}

	o.entries = entries
	if o.follow {
		o.selected = o.lastSelectable()
	} else if selectedKey != "" {
		for i := range o.entries {
			if o.entries[i].key == selectedKey {
				o.selected = i
				break
			}
		}
	}
	if o.selected < 0 || o.selected >= len(o.entries) || !o.entries[o.selected].selectable {
		next := min(o.selected, len(o.entries)-1)
		for next >= 0 && !o.entries[next].selectable {
			next--
		}
		if next < 0 {
			next = o.firstSelectable()
		}
		o.selected = next
	}
	o.updateMatches(false)
}

func (o *transcriptOverlay) firstSelectable() int {
	for i := range o.entries {
		if o.entries[i].selectable {
			return i
		}
	}
	return -1
}

func (o *transcriptOverlay) lastSelectable() int {
	for i := len(o.entries) - 1; i >= 0; i-- {
		if o.entries[i].selectable {
			return i
		}
	}
	return -1
}

func (o *transcriptOverlay) moveSelection(delta int) {
	if len(o.entries) == 0 {
		return
	}
	i := o.selected
	for {
		i += delta
		if i < 0 || i >= len(o.entries) {
			return
		}
		if o.entries[i].selectable {
			o.selected = i
			o.lineOffset = 0
			o.follow = false
			return
		}
	}
}

// step scrolls line-by-line through a selected entry taller than the viewport
// and moves the selection once its edge is reached.
func (o *transcriptOverlay) step(delta int) {
	if o.lineMax > 0 {
		current := o.lineOffset
		if o.follow {
			current = o.lineMax
		}
		next := min(max(current+delta, 0), o.lineMax)
		if next != current {
			o.lineOffset = next
			o.follow = false
			return
		}
	}
	o.moveSelection(delta)
}

func (o *transcriptOverlay) updateMatches(move bool) {
	o.matches = o.matches[:0]
	query := strings.ToLower(strings.TrimSpace(o.query))
	if query == "" {
		o.matchPos = 0
		return
	}
	for i, entry := range o.entries {
		if entry.selectable && strings.Contains(strings.ToLower(entry.raw), query) {
			o.matches = append(o.matches, i)
		}
	}
	if len(o.matches) == 0 {
		o.matchPos = 0
		return
	}
	for i, index := range o.matches {
		if index >= o.selected {
			o.matchPos = i
			if move {
				o.selected = index
				o.lineOffset = 0
				o.follow = false
			}
			return
		}
	}
	o.matchPos = 0
	if move {
		o.selected = o.matches[0]
		o.lineOffset = 0
		o.follow = false
	}
}

func (o *transcriptOverlay) jumpMatch(delta int) {
	if len(o.matches) == 0 {
		return
	}
	o.matchPos = (o.matchPos + delta + len(o.matches)) % len(o.matches)
	o.selected = o.matches[o.matchPos]
	o.lineOffset = 0
	o.follow = false
}

func (o *transcriptOverlay) HandlePaste(text string) bool {
	if !o.searching {
		return false
	}
	o.query = appendPastedSearchQuery(o.query, text)
	o.updateMatches(true)
	return true
}

func (o *transcriptOverlay) toggleSelected() {
	if o.selected < 0 || o.selected >= len(o.entries) {
		return
	}
	entry := o.entries[o.selected]
	if entry.kind == transcriptTool || entry.kind == transcriptReasoning {
		o.expanded[entry.key] = !o.expanded[entry.key]
		delete(o.cache, entry.key)
	}
}

func (o *transcriptOverlay) toggleAll() {
	expand := false
	for _, entry := range o.entries {
		if (entry.kind == transcriptTool || entry.kind == transcriptReasoning) && !o.expanded[entry.key] {
			expand = true
			break
		}
	}
	for _, entry := range o.entries {
		if entry.kind == transcriptTool || entry.kind == transcriptReasoning {
			o.expanded[entry.key] = expand
			delete(o.cache, entry.key)
		}
	}
}

func (o *transcriptOverlay) HandleKey(ev inline.KeyEvent) bool {
	o.notice = ""

	if ev.Key == inline.KeyCtrl && ev.Rune == 'o' {
		return true
	}

	if o.searching {
		switch ev.Key {
		case inline.KeyEnter:
			o.searching = false
			o.updateMatches(true)
		case inline.KeyEsc:
			o.query = ""
			o.searching = false
			o.updateMatches(false)
		case inline.KeyBackspace:
			if o.query != "" {
				_, size := utf8.DecodeLastRuneInString(o.query)
				o.query = o.query[:len(o.query)-size]
				o.updateMatches(true)
			}
		case inline.KeyRune:
			if !ev.Alt {
				o.query += string(ev.Rune)
				o.updateMatches(true)
			}
		case inline.KeyCtrl:
			if ev.Rune == 'c' {
				return true
			}
		}
		return false
	}

	switch ev.Key {
	case inline.KeyEsc:
		if o.query != "" {
			o.query = ""
			o.updateMatches(false)
			return false
		}
		return true
	case inline.KeyUp:
		o.step(-1)
	case inline.KeyDown:
		o.step(1)
	case inline.KeyPgUp:
		for range max(1, (o.height-4)/3) {
			o.moveSelection(-1)
		}
	case inline.KeyPgDn:
		for range max(1, (o.height-4)/3) {
			o.moveSelection(1)
		}
	case inline.KeyHome:
		o.selected = o.firstSelectable()
		o.lineOffset = 0
		o.follow = false
	case inline.KeyEnd:
		o.selected = o.lastSelectable()
		o.lineOffset = 0
		o.follow = true
	case inline.KeyEnter:
		o.toggleSelected()
	case inline.KeyRune:
		switch ev.Rune {
		case '/':
			o.searching = true
			o.query = ""
		case 'j':
			o.step(1)
		case 'k':
			o.step(-1)
		case 'g':
			o.selected = o.firstSelectable()
			o.lineOffset = 0
			o.follow = false
		case 'G':
			o.selected = o.lastSelectable()
			o.lineOffset = 0
			o.follow = true
		case 'n':
			o.jumpMatch(1)
		case 'N':
			o.jumpMatch(-1)
		case 'e', ' ':
			o.toggleSelected()
		case 'E':
			o.toggleAll()
		case 'y':
			if o.selected >= 0 && o.selected < len(o.entries) {
				o.app.copyTextToClipboard(o.entries[o.selected].raw)
				o.notice = "copied"
			}
		case 'q':
			return true
		}
	case inline.KeyCtrl:
		if ev.Rune == 'c' {
			return true
		}
	}
	return false
}

func (o *transcriptOverlay) HandleMouse(ev inline.MouseEvent) {
	if ev.Kind != inline.MouseWheel {
		return
	}
	delta := ev.WheelDelta
	if delta < 0 {
		delta = -delta
		for range delta * 2 {
			o.step(-1)
		}
		return
	}
	for range delta * 2 {
		o.step(1)
	}
}

func highlightTranscriptMatch(line, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return line
	}
	plain := ansi.Strip(line)
	lower := strings.ToLower(plain)
	needle := strings.ToLower(query)
	// Unicode case folding can change byte length. Searching still works in
	// that case; skip column highlighting rather than mis-map byte offsets.
	if len(lower) != len(plain) || len(needle) != len(query) {
		return line
	}
	for from := len(lower); ; {
		index := strings.LastIndex(lower[:from], needle)
		if index < 0 {
			break
		}
		start := ansi.Width(plain[:index])
		end := start + ansi.Width(plain[index:index+len(needle)])
		line = ansi.Highlight(line, start, end, ansi.Underline+ansi.Bold)
		from = index
	}
	return line
}

func (o *transcriptOverlay) entryLines(entry transcriptEntry, width int, selected bool) []string {
	expanded := o.expanded[entry.key]
	signature := theme.Default.Signature()
	if cached, ok := o.cache[entry.key]; ok && cached.raw == entry.raw && cached.width == width &&
		cached.expanded == expanded && cached.theme == signature && cached.selected == selected && cached.query == o.query {
		return cached.lines
	}

	inner := max(10, width-2)
	lines := entry.render(inner, expanded)
	for i := range lines {
		// Keep spacer rows truly empty. Prefixing them with the inspector gutter
		// makes transcript spacing differ from the main chat and leaves visible
		// selection/search artifacts on otherwise blank rows.
		if ansi.Width(lines[i]) == 0 {
			lines[i] = ""
			continue
		}
		prefix := "  "
		if selected && i == 0 {
			prefix = colored(theme.Default.Cyan, "▌ ")
		}
		lines[i] = prefix + lines[i]
		if o.query != "" {
			lines[i] = highlightTranscriptMatch(lines[i], o.query)
		}
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	o.cache[entry.key] = transcriptCache{
		raw: entry.raw, width: width, expanded: expanded, theme: signature,
		selected: selected, query: o.query, lines: lines,
	}
	return lines
}

// bodyLines composes transcript entries with the same cellFlow used by the
// committed chat and streaming tail. Entry rendering remains inspector-aware
// (selection gutters, expansion, search), while the rhythm between entries is
// shared with the normal conversation view.
func (o *transcriptOverlay) bodyLines(width int) (body []string, starts, ends []int) {
	starts = make([]int, len(o.entries))
	ends = make([]int, len(o.entries))
	flow := cellFlow{}

	for i, entry := range o.entries {
		lines := o.entryLines(entry, width, i == o.selected)
		multiline := len(lines) > 1

		var gap bool
		switch entry.kind {
		case transcriptTool, transcriptLiveTool:
			gap = flow.beforeTool(multiline)
		case transcriptReasoning:
			gap = flow.beforeThought(multiline)
		default:
			gap = flow.gap()
		}
		if gap {
			body = append(body, "")
		}

		starts[i] = len(body)
		body = append(body, lines...)
		ends[i] = len(body)
	}

	return body, starts, ends
}

func (o *transcriptOverlay) Render(width, height int) []string {
	o.height = height
	o.buildEntries()

	t := theme.Default
	contentRows := max(0, height-3)
	body, starts, ends := o.bodyLines(width)
	if len(body) == 0 {
		body = []string{"  " + dim("No transcript yet…")}
	}

	o.lineMax = 0
	if o.selected >= 0 && o.selected < len(starts) {
		o.lineMax = max(0, ends[o.selected]-starts[o.selected]-contentRows)
	}

	if o.follow {
		o.offset = max(0, len(body)-contentRows)
	} else if o.selected >= 0 && o.selected < len(starts) {
		if o.lineMax > 0 {
			o.lineOffset = min(max(o.lineOffset, 0), o.lineMax)
			o.offset = starts[o.selected] + o.lineOffset
		} else {
			o.lineOffset = 0
			if starts[o.selected] < o.offset {
				o.offset = starts[o.selected]
			}
			if ends[o.selected] > o.offset+contentRows {
				o.offset = ends[o.selected] - contentRows
			}
		}
	}
	maxOffset := max(0, len(body)-contentRows)
	o.offset = min(max(o.offset, 0), maxOffset)

	matchStatus := ""
	if o.query != "" {
		if len(o.matches) == 0 {
			matchStatus = " · no matches"
		} else {
			matchStatus = fmt.Sprintf(" · %d/%d", o.matchPos+1, len(o.matches))
		}
	}
	title := bold("transcript") + dim(fmt.Sprintf("  %d entries", len(o.entries)))
	if o.searching || o.query != "" {
		title += colored(t.Cyan, "  /"+o.query) + dim(matchStatus)
	}
	if o.notice != "" {
		title += colored(t.Green, "  · "+o.notice)
	}

	percent := 100
	if len(body) > contentRows && len(body) > 0 {
		percent = min(100, (o.offset+contentRows)*100/len(body))
	}
	pct := fmt.Sprintf(" %d%% ", percent)
	ruleWidth := max(10, width-ansi.Width(pct))
	rule := colored(t.BrBlack, strings.Repeat("─", ruleWidth)) + dim(pct)
	lines := []string{ansi.Truncate(title, width, "…"), ansi.Truncate(rule, width, "…")}

	end := min(len(body), o.offset+contentRows)
	lines = append(lines, body[o.offset:end]...)
	for len(lines) < height-1 {
		lines = append(lines, "")
	}

	hints := "↑↓ select · enter expand · / search · y copy · E all · ctrl+o close"
	if o.searching {
		hints = "type to search · enter accept · esc clear"
	}
	lines = append(lines, ansi.Truncate(dim(hints), width, "…"))
	return lines
}
