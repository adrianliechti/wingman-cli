package code

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/lsp"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

type fileDiagnostics struct {
	Path        string
	Diagnostics []lsp.Diagnostic
	Errors      int
	Warnings    int
}

func (a *App) showDiagnosticsView() {
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()

		files, err := a.collectDiagnostics(ctx)

		a.post(func() {
			a.showDiagnosticsOverlay(files, err)
		})
	}()
}

func (a *App) showDiagnosticsOverlay(files []fileDiagnostics, collectErr error) {
	t := theme.Default

	if collectErr != nil {
		a.appendChat(cellError("Diagnostics failed", collectErr.Error(), a.width()))
	}

	if len(files) == 0 {
		a.showToast("No diagnostics found", t.BrBlack)
		a.invalidate()
		return
	}

	totalErrors, totalWarnings := 0, 0
	for _, f := range files {
		totalErrors += f.Errors
		totalWarnings += f.Warnings
	}

	status := dim(fmt.Sprintf("%d file(s)", len(files))) + "  " +
		colored(t.Red, fmt.Sprintf("%d errors", totalErrors)) + "  " +
		colored(t.Yellow, fmt.Sprintf("%d warnings", totalWarnings))

	item := func(selected bool, i int) string {
		f := files[i]

		iconColor := t.Yellow
		if f.Errors > 0 {
			iconColor = t.Red
		}

		stats := colored(t.Red, fmt.Sprintf("%d", f.Errors))
		if f.Warnings > 0 {
			stats += " " + colored(t.Yellow, fmt.Sprintf("%d", f.Warnings))
		}

		if selected {
			return colored(t.Cyan, "→ ") + colored(iconColor, "●") + " " + colored(t.Cyan, f.Path) + " " + stats
		}
		return "  " + colored(iconColor, "●") + " " + f.Path + " " + stats
	}

	content := func(i int) []string {
		var lines []string

		for _, d := range files[i].Diagnostics {
			var severityColor = t.BrBlack
			severityLabel := "Hint"

			switch d.Severity {
			case lsp.DiagnosticSeverityError:
				severityColor, severityLabel = t.Red, "Error"
			case lsp.DiagnosticSeverityWarning:
				severityColor, severityLabel = t.Yellow, "Warning"
			case lsp.DiagnosticSeverityInformation:
				severityColor, severityLabel = t.Cyan, "Info"
			}

			source := ""
			if d.Source != "" {
				source = dim(d.Source) + " "
			}

			lines = append(lines, colored(severityColor, severityLabel)+" "+
				dim(fmt.Sprintf("L%d:%d", d.Range.Start.Line+1, d.Range.Start.Character+1))+" "+
				source+d.Message)
		}

		return lines
	}
	search := func(i int) string {
		text := files[i].Path
		for _, diagnostic := range files[i].Diagnostics {
			text += "\n" + diagnostic.Source + " " + diagnostic.Message
		}
		return text
	}

	a.openOverlay(newTwoPaneOverlay("problems", status, len(files), item, content, search))
}

func (a *App) collectDiagnostics(ctx context.Context) ([]fileDiagnostics, error) {
	workDir := a.agent.Workspace().RootPath
	var files []fileDiagnostics

	for path, diags := range a.agent.Workspace().Diagnostics(ctx) {
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

	slices.SortFunc(files, func(a, b fileDiagnostics) int {
		if a.Errors != b.Errors {
			return cmp.Compare(b.Errors, a.Errors)
		}
		return cmp.Compare(a.Path, b.Path)
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
