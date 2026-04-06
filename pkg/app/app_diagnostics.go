package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-agent/pkg/agent/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
)

type fileDiagnostics struct {
	Path        string
	Diagnostics []lsp.Diagnostic
	Errors      int
	Warnings    int
}

// showDiagnosticsView collects diagnostics in the background and displays
// the results when ready.
func (a *App) showDiagnosticsView() {
	t := theme.Default
	fmt.Fprint(a.chatView, a.formatNotice("Collecting diagnostics…", t.BrBlack))

	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()

		files, err := a.collectDiagnostics(ctx)

		a.app.QueueUpdateDraw(func() {
			if err != nil {
				fmt.Fprint(a.chatView, a.formatNotice(fmt.Sprintf("Diagnostics: %v", err), t.Yellow))
				return
			}

			if len(files) == 0 {
				fmt.Fprint(a.chatView, a.formatNotice("No diagnostics found", t.BrBlack))
				return
			}

			a.showDiagnosticsModal(files)
		})
	}()
}

func (a *App) showDiagnosticsModal(files []fileDiagnostics) {
	t := theme.Default
	a.activeModal = ModalDiagnostics

	// Stats
	totalErrors, totalWarnings := 0, 0
	for _, f := range files {
		totalErrors += f.Errors
		totalWarnings += f.Warnings
	}

	selectedIndex := 0

	// === FILE LIST ===
	fileListView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	fileListView.SetBackgroundColor(tcell.ColorDefault)

	renderFileList := func() {
		fileListView.Clear()
		var sb strings.Builder

		for i, f := range files {
			var icon string
			var iconColor tcell.Color

			if f.Errors > 0 {
				icon = "●"
				iconColor = t.Red
			} else {
				icon = "●"
				iconColor = t.Yellow
			}

			stats := fmt.Sprintf("[%s]%d[-]", t.Red, f.Errors)
			if f.Warnings > 0 {
				stats += fmt.Sprintf(" [%s]%d[-]", t.Yellow, f.Warnings)
			}

			if i == selectedIndex {
				fmt.Fprintf(&sb, "  [%s]▶[-] [%s]%s[-] [%s::b]%s[-::-] %s\n",
					t.Cyan, iconColor, icon, t.Cyan, f.Path, stats)
			} else {
				fmt.Fprintf(&sb, "    [%s]%s[-] [%s]%s[-] %s\n",
					iconColor, icon, t.Foreground, f.Path, stats)
			}
		}

		fileListView.SetText(sb.String())
		fileListView.ScrollTo(selectedIndex, 0)
	}

	// === DIAGNOSTICS CONTENT ===
	diagContentView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true)
	diagContentView.SetBackgroundColor(tcell.ColorDefault)

	renderDiagContent := func() {
		if selectedIndex < 0 || selectedIndex >= len(files) {
			return
		}

		f := files[selectedIndex]
		diagContentView.Clear()

		var sb strings.Builder
		for _, d := range f.Diagnostics {
			var severityColor tcell.Color
			var severityLabel string

			switch d.Severity {
			case lsp.DiagnosticSeverityError:
				severityColor = t.Red
				severityLabel = "Error"
			case lsp.DiagnosticSeverityWarning:
				severityColor = t.Yellow
				severityLabel = "Warning"
			case lsp.DiagnosticSeverityInformation:
				severityColor = t.Cyan
				severityLabel = "Info"
			default:
				severityColor = t.BrBlack
				severityLabel = "Hint"
			}

			source := ""
			if d.Source != "" {
				source = fmt.Sprintf("[%s]%s[-] ", t.BrBlack, d.Source)
			}

			fmt.Fprintf(&sb, "  [%s]%s[-] [%s]L%d:%d[-] %s%s\n",
				severityColor, severityLabel,
				t.BrBlack, d.Range.Start.Line+1, d.Range.Start.Character+1,
				source, d.Message)
		}

		diagContentView.SetText(sb.String())
		diagContentView.ScrollToBeginning()
	}

	renderFileList()
	renderDiagContent()

	// === BOTTOM BAR ===
	hintBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	hintBar.SetBackgroundColor(tcell.ColorDefault)

	statusBar := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignRight)
	statusBar.SetBackgroundColor(tcell.ColorDefault)

	fmt.Fprintf(statusBar, "[%s]%d file(s)[-]  [%s]%d errors[-]  [%s]%d warnings[-]",
		t.BrBlack, len(files), t.Red, totalErrors, t.Yellow, totalWarnings)

	focusedPanel := 0

	updateHintBar := func() {
		hintBar.Clear()
		if focusedPanel == 0 {
			fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]tab[-] [%s]switch[-]  [%s]↑↓/jk[-] [%s]select[-]",
				t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)
		} else {
			fmt.Fprintf(hintBar, "[%s]esc[-] [%s]close[-]  [%s]tab[-] [%s]switch[-]  [%s]↑↓/jk[-] [%s]scroll[-]",
				t.BrBlack, t.Foreground, t.BrBlack, t.Foreground, t.BrBlack, t.Foreground)
		}
	}
	updateHintBar()

	// === LAYOUT ===
	separator := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	separator.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for i := y; i < y+height; i++ {
			screen.SetContent(x, i, '│', nil, tcell.StyleDefault.Foreground(t.BrBlack))
		}
		return x + 1, y, width - 1, height
	})

	panelsContainer := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(fileListView, 40, 0, true).
		AddItem(separator, 1, 0, false).
		AddItem(diagContentView, 0, 1, false)
	panelsContainer.SetBackgroundColor(tcell.ColorDefault)

	leftMargin, rightMargin := a.getMargins()
	inputLeftMargin, inputRightMargin := a.getInputMargins()

	contentWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, leftMargin, 0, false).
		AddItem(panelsContainer, 0, 1, true).
		AddItem(nil, rightMargin, 0, false)
	contentWithMargins.SetBackgroundColor(tcell.ColorDefault)

	bottomBar := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(hintBar, 0, 1, false).
		AddItem(statusBar, 40, 0, false)
	bottomBar.SetBackgroundColor(tcell.ColorDefault)
	bottomBar.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	bottomBarWithMargins := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, inputLeftMargin, 0, false).
		AddItem(bottomBar, 0, 1, false).
		AddItem(nil, inputRightMargin, 0, false)
	bottomBarWithMargins.SetBackgroundColor(tcell.ColorDefault)
	bottomBarWithMargins.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	topSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	statusSpacer := tview.NewBox().SetBackgroundColor(tcell.ColorDefault)
	statusSpacer.SetDrawFunc(func(screen tcell.Screen, x, y, width, height int) (int, int, int, int) {
		for row := y; row < y+height; row++ {
			for col := x; col < x+width; col++ {
				screen.SetContent(col, row, ' ', nil, tcell.StyleDefault)
			}
		}
		return x, y, width, height
	})

	container := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(topSpacer, 1, 0, false).
		AddItem(contentWithMargins, 0, 1, true).
		AddItem(statusSpacer, 1, 0, false).
		AddItem(bottomBarWithMargins, 1, 0, false)
	container.SetBackgroundColor(tcell.ColorDefault)

	// === INPUT HANDLING ===
	fileListView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			if selectedIndex > 0 {
				selectedIndex--
				renderFileList()
				renderDiagContent()
			}
			return nil
		case tcell.KeyDown:
			if selectedIndex < len(files)-1 {
				selectedIndex++
				renderFileList()
				renderDiagContent()
			}
			return nil
		}
		switch event.Rune() {
		case 'k':
			if selectedIndex > 0 {
				selectedIndex--
				renderFileList()
				renderDiagContent()
			}
			return nil
		case 'j':
			if selectedIndex < len(files)-1 {
				selectedIndex++
				renderFileList()
				renderDiagContent()
			}
			return nil
		}
		return event
	})

	diagContentView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, col := diagContentView.GetScrollOffset()
		switch event.Key() {
		case tcell.KeyUp:
			if row > 0 {
				diagContentView.ScrollTo(row-1, col)
			}
			return nil
		case tcell.KeyDown:
			diagContentView.ScrollTo(row+1, col)
			return nil
		}
		switch event.Rune() {
		case 'j':
			diagContentView.ScrollTo(row+1, col)
			return nil
		case 'k':
			if row > 0 {
				diagContentView.ScrollTo(row-1, col)
			}
			return nil
		case 'g':
			diagContentView.ScrollToBeginning()
			return nil
		case 'G':
			diagContentView.ScrollToEnd()
			return nil
		}
		return event
	})

	container.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab {
			if focusedPanel == 0 {
				focusedPanel = 1
				a.app.SetFocus(diagContentView)
			} else {
				focusedPanel = 0
				a.app.SetFocus(fileListView)
			}
			updateHintBar()
			return nil
		}
		return event
	})

	if a.pages != nil {
		a.pages.AddPage("diagnostics", container, true, true)
		a.app.SetFocus(fileListView)
	}
}

func (a *App) closeDiagnosticsView() {
	a.activeModal = ModalNone
	if a.pages != nil {
		a.pages.RemovePage("diagnostics")
		a.app.SetFocus(a.input)
	}
}

func (a *App) collectDiagnostics(ctx context.Context) ([]fileDiagnostics, error) {
	if a.bridge.IsConnected() {
		return a.collectBridgeDiagnostics(ctx)
	}
	return a.collectLocalDiagnostics(ctx)
}

func (a *App) collectBridgeDiagnostics(ctx context.Context) ([]fileDiagnostics, error) {
	result, err := a.bridge.GetDiagnostics(ctx, "")
	if err != nil {
		return nil, err
	}

	if result == "" || result == "{}" {
		return nil, nil
	}

	// Bridge returns workspace diagnostics as { "path": [...diags] }
	var raw map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(result), &raw); err != nil {
		return nil, nil
	}

	workDir := a.lspManager.WorkingDir()
	var files []fileDiagnostics

	for path, rawDiags := range raw {
		if len(rawDiags) == 0 {
			continue
		}

		var diags []lsp.Diagnostic
		for _, rd := range rawDiags {
			var d struct {
				Range struct {
					Start struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"start"`
					End struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					} `json:"end"`
				} `json:"range"`
				Severity string `json:"severity"`
				Message  string `json:"message"`
				Source   string `json:"source"`
			}
			if err := json.Unmarshal(rd, &d); err != nil {
				continue
			}

			severity := lsp.DiagnosticSeverityError
			switch d.Severity {
			case "Warning":
				severity = lsp.DiagnosticSeverityWarning
			case "Info":
				severity = lsp.DiagnosticSeverityInformation
			case "Hint":
				severity = lsp.DiagnosticSeverityHint
			}

			diags = append(diags, lsp.Diagnostic{
				Range: lsp.Range{
					Start: lsp.Position{Line: d.Range.Start.Line, Character: d.Range.Start.Character},
					End:   lsp.Position{Line: d.Range.End.Line, Character: d.Range.End.Character},
				},
				Severity: severity,
				Source:   d.Source,
				Message:  d.Message,
			})
		}

		if len(diags) == 0 {
			continue
		}

		fd := fileDiagnostics{
			Path:        relPath(workDir, path),
			Diagnostics: diags,
		}
		for _, d := range diags {
			if d.Severity == lsp.DiagnosticSeverityError {
				fd.Errors++
			} else if d.Severity == lsp.DiagnosticSeverityWarning {
				fd.Warnings++
			}
		}
		files = append(files, fd)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Errors != files[j].Errors {
			return files[i].Errors > files[j].Errors
		}
		return files[i].Path < files[j].Path
	})

	return files, nil
}

func (a *App) collectLocalDiagnostics(ctx context.Context) ([]fileDiagnostics, error) {
	allDiags := a.lspManager.CollectAllDiagnostics(ctx)
	if len(allDiags) == 0 {
		return nil, nil
	}

	workDir := a.lspManager.WorkingDir()
	var files []fileDiagnostics

	for path, diags := range allDiags {
		if len(diags) == 0 {
			continue
		}

		fd := fileDiagnostics{
			Path:        relPath(workDir, path),
			Diagnostics: diags,
		}
		for _, d := range diags {
			if d.Severity == lsp.DiagnosticSeverityError {
				fd.Errors++
			} else if d.Severity == lsp.DiagnosticSeverityWarning {
				fd.Warnings++
			}
		}
		files = append(files, fd)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Errors != files[j].Errors {
			return files[i].Errors > files[j].Errors
		}
		return files[i].Path < files[j].Path
	})

	return files, nil
}

func relPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
