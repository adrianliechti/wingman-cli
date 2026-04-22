package app

import (
	"bufio"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/rivo/tview"
	"github.com/sahilm/fuzzy"

	"github.com/adrianliechti/wingman-agent/pkg/ui/theme"
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

// fileMatches implements fuzzy.Source for []fileMatch
type fileMatches []fileMatch

func (f fileMatches) String(i int) string { return f[i].Path }
func (f fileMatches) Len() int            { return len(f) }

// collectFiles walks the workspace and collects all file paths
func (a *App) collectFiles() []fileMatch {
	var files []fileMatch
	fsys := a.agent.Root.FS()

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

			newPatterns := loadGitignore(fsys, strings.Split(path, "/"))

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
		gitignorePath = pathpkg.Join(append(domain, ".gitignore")...)
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

// fuzzyFilterFiles performs fuzzy matching on file paths, sorted by score.
func fuzzyFilterFiles(files []fileMatch, query string) []fileMatch {
	if query == "" {
		if len(files) > maxFileResults {
			return files[:maxFileResults]
		}

		return files
	}

	results := fuzzy.FindFrom(query, fileMatches(files))

	var matches []fileMatch

	for _, r := range results {
		matches = append(matches, files[r.Index])

		if len(matches) >= maxFileResults {
			break
		}
	}

	return matches
}

// showFilePicker displays a file selection picker with fuzzy filtering and multi-select.
func (a *App) showFilePicker(initialQuery string, onSelect func(paths []string)) {
	go func() {
		files := a.collectFiles()

		a.app.QueueUpdateDraw(func() {
			if a.hasActiveModal() {
				return
			}

			a.activeModal = ModalFilePicker
			t := theme.Default
			filtered := fuzzyFilterFiles(files, initialQuery)
			selected := make(map[string]bool)

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

			// Function to render a list item with selection state
			itemText := func(f fileMatch) string {
				if selected[f.Path] {
					return fmt.Sprintf("  [%s]●[-] %s", t.Cyan, f.Path)
				}

				return "  " + f.Path
			}

			// Function to update list items
			updateList := func(query string) {
				list.Clear()
				filtered = fuzzyFilterFiles(files, query)

				for _, f := range filtered {
					list.AddItem(itemText(f), "", 0, nil)
				}

				if len(filtered) > 0 {
					list.SetCurrentItem(0)
				}
			}

			// Refresh list item text without changing filtered results
			refreshList := func() {
				for i, f := range filtered {
					list.SetItemText(i, itemText(f), "")
				}
			}

			// Toggle selection of current item
			toggleCurrent := func() {
				idx := list.GetCurrentItem()
				if idx >= 0 && idx < len(filtered) {
					path := filtered[idx].Path
					selected[path] = !selected[path]

					if !selected[path] {
						delete(selected, path)
					}

					refreshList()
				}
			}

			// Initial population
			updateList(initialQuery)

			// Update list on text change
			searchInput.SetChangedFunc(func(text string) {
				updateList(text)
			})

			// Handle selection
			selectFiles := func() {
				var paths []string

				// Collect all toggled files (across all searches)
				for path := range selected {
					paths = append(paths, path)
				}
				sort.Strings(paths)

				// If none toggled, use the highlighted item
				if len(paths) == 0 {
					idx := list.GetCurrentItem()
					if idx >= 0 && idx < len(filtered) {
						paths = []string{filtered[idx].Path}
					}
				}

				a.closeFilePicker()

				if onSelect != nil && len(paths) > 0 {
					onSelect(paths)
				}
			}

			list.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
				selectFiles()
			})

			searchInput.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
				switch event.Key() {
				case tcell.KeyCtrlC, tcell.KeyEscape:
					a.closeFilePicker()

					return nil
				case tcell.KeyEnter:
					selectFiles()

					return nil
				case tcell.KeyTab:
					toggleCurrent()

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
	}()
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
