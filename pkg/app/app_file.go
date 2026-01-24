package app

import (
	"bufio"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/rivo/tview"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

// Directories to skip when collecting files
var defaultIgnoreDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".svn":         true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}

const (
	maxFileResults   = 50
	filePickerPageID = "file-picker"
)

// fileMatch represents a matched file for the picker
type fileMatch struct {
	Path string
	Name string
}

// collectFiles walks the workspace and collects all file paths
func (a *App) collectFiles() []fileMatch {
	var files []fileMatch
	fsys := a.config.Environment.Root.FS()

	var allPatterns []gitignore.Pattern
	allPatterns = append(allPatterns, loadGitignore(fsys, nil)...)
	matcher := gitignore.NewMatcher(allPatterns)

	fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Skip ignored directories
		if d.IsDir() {
			name := d.Name()

			if name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}

			if defaultIgnoreDirs[name] {
				return filepath.SkipDir
			}

			relPath := filepath.ToSlash(path)
			pathParts := strings.Split(relPath, "/")

			if matcher.Match(pathParts, true) {
				return filepath.SkipDir
			}

			newPatterns := loadGitignore(fsys, strings.Split(path, string(filepath.Separator)))

			if len(newPatterns) > 0 {
				allPatterns = append(allPatterns, newPatterns...)
				matcher = gitignore.NewMatcher(allPatterns)
			}

			return nil
		}

		// Skip hidden files
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		relPath := filepath.ToSlash(path)
		pathParts := strings.Split(relPath, "/")

		if matcher.Match(pathParts, false) {
			return nil
		}

		files = append(files, fileMatch{
			Path: path,
			Name: d.Name(),
		})

		return nil
	})

	return files
}

func loadGitignore(fsys fs.FS, domain []string) []gitignore.Pattern {
	gitignorePath := ".gitignore"

	if len(domain) > 0 {
		gitignorePath = filepath.Join(append(domain, ".gitignore")...)
	}

	f, err := fsys.Open(gitignorePath)

	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}

	return patterns
}

// filterFiles performs case-insensitive substring matching on filenames
func filterFiles(files []fileMatch, query string) []fileMatch {
	if query == "" {
		// Return first N files when no query
		if len(files) > maxFileResults {
			return files[:maxFileResults]
		}

		return files
	}

	query = strings.ToLower(query)
	var matches []fileMatch

	for _, f := range files {
		// Match against both filename and path
		nameLower := strings.ToLower(f.Name)
		pathLower := strings.ToLower(f.Path)

		if strings.Contains(nameLower, query) || strings.Contains(pathLower, query) {
			matches = append(matches, f)

			if len(matches) >= maxFileResults {
				break
			}
		}
	}

	return matches
}

// showFilePicker displays a file selection picker with filtering.
// Must be called from a goroutine as it collects files before showing UI.
func (a *App) showFilePicker(initialQuery string, onSelect func(path string)) {
	files := a.collectFiles()

	a.app.QueueUpdateDraw(func() {
		if a.hasActiveModal() {
			return
		}

		a.activeModal = ModalFilePicker
		t := theme.Default
		filtered := filterFiles(files, initialQuery)

		// Create the list
		list := tview.NewList().
			ShowSecondaryText(false)
		list.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
		list.SetMainTextColor(t.Foreground)
		list.SetSelectedTextColor(t.Cyan)
		list.SetSelectedBackgroundColor(tview.Styles.PrimitiveBackgroundColor)

		// Create search input
		searchInput := tview.NewInputField()
		searchInput.SetLabel("@ ")
		searchInput.SetLabelColor(t.Cyan)
		searchInput.SetFieldBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
		searchInput.SetFieldTextColor(t.Foreground)
		searchInput.SetText(initialQuery)

		// Function to update list items
		updateList := func(query string) {
			list.Clear()
			filtered = filterFiles(files, query)

			for _, f := range filtered {
				list.AddItem("  "+f.Path, "", 0, nil)
			}

			if len(filtered) > 0 {
				list.SetCurrentItem(0)
			}
		}

		// Initial population
		updateList(initialQuery)

		// Update list on text change
		searchInput.SetChangedFunc(func(text string) {
			updateList(text)
		})

		// Handle selection
		selectFile := func() {
			idx := list.GetCurrentItem()

			if idx >= 0 && idx < len(filtered) {
				a.closeFilePicker()

				if onSelect != nil {
					onSelect(filtered[idx].Path)
				}
			}
		}

		list.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
			selectFile()
		})

		searchInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			switch event.Key() {
			case tcell.KeyCtrlC, tcell.KeyEscape:
				a.closeFilePicker()

				return nil
			case tcell.KeyEnter, tcell.KeyTab:
				selectFile()

				return nil
			case tcell.KeyDown:

				if idx := list.GetCurrentItem(); idx < list.GetItemCount()-1 {
					list.SetCurrentItem(idx + 1)
				}

				return nil
			case tcell.KeyUp:

				if idx := list.GetCurrentItem(); idx > 0 {
					list.SetCurrentItem(idx - 1)
				}

				return nil
			}

			return event
		})

		list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			if event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyCtrlC {
				a.closeFilePicker()

				return nil
			}

			if event.Key() == tcell.KeyRune {
				searchInput.SetText(searchInput.GetText() + string(event.Rune()))

				return nil
			}

			if event.Key() == tcell.KeyBackspace || event.Key() == tcell.KeyBackspace2 {
				if t := searchInput.GetText(); len(t) > 0 {
					searchInput.SetText(t[:len(t)-1])
				}

				return nil
			}

			return event
		})

		// Build modal
		boxWidth := 60
		boxHeight := min(len(filtered)+6, 20)

		content := tview.NewFlex().SetDirection(tview.FlexRow)
		content.AddItem(searchInput, 1, 0, true)
		content.AddItem(list, 0, 1, false)

		box := tview.NewFlex().SetDirection(tview.FlexRow)
		box.Box = tview.NewBox()
		box.AddItem(content, 0, 1, true)
		box.SetBorder(true)
		box.SetBorderColor(t.Cyan)
		box.SetTitle(" Select File ")
		box.SetTitleColor(t.Cyan)
		box.SetTitleAlign(tview.AlignCenter)
		box.SetBackgroundColor(tview.Styles.PrimitiveBackgroundColor)
		box.SetBorderPadding(1, 1, 2, 2)

		modal := tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(box, boxHeight, 0, true).
				AddItem(nil, 0, 1, false), boxWidth, 0, true).
			AddItem(nil, 0, 1, false)
		modal.SetBackgroundColor(tcell.ColorDefault)

		a.pages.AddPage(filePickerPageID, modal, true, true)
		a.app.SetFocus(searchInput)
	})
}

func (a *App) closeFilePicker() {
	a.activeModal = ModalNone

	if a.pages != nil {
		a.pages.RemovePage(filePickerPageID)
		a.app.SetFocus(a.input)
	}
}

// addFileToContext adds a file path to the pending context
func (a *App) addFileToContext(path string) error {
	a.pendingFiles = append(a.pendingFiles, path)
	a.updateInputHint()

	return nil
}