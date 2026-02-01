package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/server"
	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

// ServerUI represents the server TUI
type ServerUI struct {
	app       *tview.Application
	logView   *tview.TextView
	hintView  *tview.TextView
	infoView  *tview.TextView
	stopChan  chan struct{}
	width     int
	toolCount int
	addr      string
}

// NewServerUI creates a new server UI and returns both the UI and pre-wired server options
func NewServerUI() (*ServerUI, *server.Options) {
	theme.Auto()

	ui := &ServerUI{
		app:      tview.NewApplication(),
		stopChan: make(chan struct{}),
		width:    80,
	}

	ui.setup()

	opts := &server.Options{
		OnToolStart: func(ctx context.Context, name string, args string) {
			hint := extractToolHint(args)
			ui.app.QueueUpdateDraw(func() {
				ui.renderToolStart(name, hint)
			})
		},
		OnToolComplete: func(ctx context.Context, name string, args string, result string) {
			hint := extractToolHint(args)
			ui.app.QueueUpdateDraw(func() {
				ui.renderToolComplete(name, hint, result, false)
			})
		},
		OnToolError: func(ctx context.Context, name string, args string, err error) {
			hint := extractToolHint(args)
			ui.app.QueueUpdateDraw(func() {
				ui.renderToolComplete(name, hint, err.Error(), true)
			})
		},
		OnPromptUser: func(ctx context.Context, prompt string) (bool, error) {
			// Auto-approve for server mode
			return true, nil
		},
	}

	return ui, opts
}

func (ui *ServerUI) setup() {
	t := theme.Default

	// Logo view at the top
	logoView := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	logoView.SetText(Logo)
	logoView.SetBackgroundColor(t.Background)

	// Log view for tool calls
	ui.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	ui.logView.SetBackgroundColor(t.Background)

	// Bottom bar with hints and info
	ui.hintView = tview.NewTextView().
		SetDynamicColors(true)
	ui.hintView.SetBackgroundColor(t.Background)
	ui.hintView.SetText(fmt.Sprintf("  [%s]Press [%s::b]Ctrl+C[-::-] [%s]to quit[-]", t.BrBlack, t.Yellow, t.BrBlack))

	ui.infoView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	ui.infoView.SetBackgroundColor(t.Background)
	ui.updateInfoView()

	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottomBar.AddItem(ui.hintView, 0, 1, false)
	bottomBar.AddItem(ui.infoView, 0, 1, false)

	// Main layout
	flex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(logoView, 8, 0, false).
		AddItem(ui.logView, 0, 1, false).
		AddItem(bottomBar, 1, 0, false)

	flex.SetBackgroundColor(t.Background)

	// Track width for formatting
	flex.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		ui.width = width
		return x, y, width, height
	})

	// Handle input
	ui.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC || event.Key() == tcell.KeyEscape {
			close(ui.stopChan)
			ui.app.Stop()
			return nil
		}
		return event
	})

	ui.app.SetRoot(flex, true)
}

// SetServerInfo updates the status bar with server info (call before Run)
func (ui *ServerUI) SetServerInfo(addr string) {
	ui.addr = addr
	ui.updateInfoView()
	ui.showConnectionGuide()
}

func (ui *ServerUI) showConnectionGuide() {
	t := theme.Default

	// Build the MCP URL from the address
	host := ui.addr
	if host == "" || host[0] == ':' {
		host = "localhost" + host
	}
	mcpURL := fmt.Sprintf("http://%s/mcp", host)

	ui.logView.Clear()
	fmt.Fprintf(ui.logView, "\n")
	fmt.Fprintf(ui.logView, "  [%s]Configure your MCP client with:[-]\n\n", t.BrBlack)
	fmt.Fprintf(ui.logView, "  [%s::b]%s[-::-]\n", t.Cyan, mcpURL)
}

func (ui *ServerUI) updateInfoView() {
	t := theme.Default

	// Show "Waiting for connections..." until first tool call
	if ui.toolCount == 0 {
		ui.infoView.SetText(fmt.Sprintf("[%s]Waiting for connections...[-]  ", t.BrBlack))
		return
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("[%s]%d tool calls[-]", t.BrBlack, ui.toolCount))

	if ui.addr != "" {
		parts = append(parts, fmt.Sprintf("[%s]%s[-]", t.Cyan, ui.addr))
	}

	ui.infoView.SetText(strings.Join(parts, fmt.Sprintf(" [%s]•[-] ", t.BrBlack)) + "  ")
}

// Run starts the UI event loop
func (ui *ServerUI) Run() error {
	return ui.app.Run()
}

// Stop stops the UI
func (ui *ServerUI) Stop() {
	close(ui.stopChan)
	ui.app.Stop()
}

// StopChan returns a channel that's closed when the UI is stopped
func (ui *ServerUI) StopChan() <-chan struct{} {
	return ui.stopChan
}

func (ui *ServerUI) renderToolStart(name string, hint string) {
	// Clear connection guide on first tool call
	if ui.toolCount == 0 {
		ui.logView.Clear()
	}

	fmt.Fprint(ui.logView, ui.formatToolProgress(name, hint))
	ui.logView.ScrollToEnd()
}

func (ui *ServerUI) renderToolComplete(name string, hint string, output string, isError bool) {
	ui.toolCount++
	ui.updateInfoView()
	fmt.Fprint(ui.logView, ui.formatToolCall(name, hint, output, isError))
	ui.logView.ScrollToEnd()
}

func (ui *ServerUI) formatToolProgress(name string, hint string) string {
	const indent = "  "

	t := theme.Default

	var result strings.Builder

	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]running...[-]", t.Yellow, name, t.BrBlack)

	if hint != "" {
		titleLine = fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]%s[-]", t.Yellow, name, t.BrBlack, tview.Escape(hint))
	}
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, t.Yellow, titleLine)

	return result.String()
}

func (ui *ServerUI) formatToolCall(name string, hint string, output string, isError bool) string {
	const indent = "  "
	const barWidth = 4

	t := theme.Default
	contentWidth := ui.width - len(indent) - barWidth

	if contentWidth < 20 {
		contentWidth = 60
	}

	barColor := t.Yellow
	if isError {
		barColor = t.Red
	}

	var result strings.Builder

	// Title line with tool name and hint
	titleLine := fmt.Sprintf("[%s::b]⚡ %s[-::-]", t.Yellow, name)
	if hint != "" {
		titleLine = fmt.Sprintf("[%s::b]⚡ %s[-::-] [%s]%s[-]", t.Yellow, name, t.BrBlack, tview.Escape(hint))
	}
	fmt.Fprintf(&result, "%s[%s]┃[-] %s\n", indent, barColor, titleLine)

	// Truncate output if too long
	if len(output) > maxToolOutputLen {
		output = output[:maxToolOutputLen] + "..."
	}

	// Output lines
	for _, line := range strings.Split(output, "\n") {
		wrapped := wrapLine(line, contentWidth)
		for _, wl := range wrapped {
			escaped := tview.Escape(wl)
			fmt.Fprintf(&result, "%s[%s]┃[-] [%s]%s[-]\n", indent, barColor, t.BrBlack, escaped)
		}
	}

	result.WriteString("\n")

	return result.String()
}

// wrapLine wraps a line to the specified width
func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	var lines []string
	for len(line) > width {
		// Find a good break point
		breakAt := width
		for i := width; i > width/2; i-- {
			if line[i] == ' ' {
				breakAt = i
				break
			}
		}
		lines = append(lines, line[:breakAt])
		line = strings.TrimLeft(line[breakAt:], " ")
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}
