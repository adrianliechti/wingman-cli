package code

import (
	"bufio"
	"fmt"
	"io/fs"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/adrianliechti/wingman-agent/pkg/agent"
	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

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

type fileMatch struct {
	Path string
	Name string
}

func (a *App) collectFiles() []fileMatch {
	var files []fileMatch
	fsys := a.agent.Workspace().Root.FS()

	var allPatterns []gitignore.Pattern
	allPatterns = append(allPatterns, loadGitignore(fsys, nil)...)
	matcher := gitignore.NewMatcher(allPatterns)

	fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

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

// contextFileID normalizes to slashes so pending paths (possibly OS-native)
// and workspace walk paths (always slash) produce the same picker ID.
func contextFileID(path string) string {
	return "file:" + filepath.ToSlash(path)
}

// applyContextSelection replaces the pending attachments with the picker
// outcome. Pending files the picker never offered (paths outside the
// workspace, ignored files) ride along in the selection and are kept.
func (a *App) applyContextSelection(images []agent.Content, files []fileMatch, ids []string) {
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}

	var content []agent.Content
	for _, existing := range a.pendingContent {
		if existing.File == nil {
			content = append(content, existing)
		}
	}
	for i, image := range images {
		if selected[fmt.Sprintf("image:%d", i)] {
			content = append(content, image)
		}
	}

	offered := make(map[string]bool, len(files))
	var selectedFiles []string
	for _, f := range files {
		id := contextFileID(f.Path)
		offered[id] = true
		if selected[id] {
			selectedFiles = append(selectedFiles, f.Path)
		}
	}
	for _, path := range a.pendingFiles {
		if id := contextFileID(path); !offered[id] && selected[id] {
			selectedFiles = append(selectedFiles, path)
		}
	}

	a.pendingContent = content
	a.pendingFiles = selectedFiles
}

func (a *App) addFileToContext(path string) error {
	if slices.Contains(a.pendingFiles, path) {
		return nil
	}
	a.pendingFiles = append(a.pendingFiles, path)

	return nil
}

func (a *App) attachmentLines(width int) []string {
	images := a.countPendingImages()
	total := images + len(a.pendingFiles)
	if total == 0 {
		return nil
	}

	t := theme.Default
	chip := func(label string) string {
		return ansi.Bg(t.Selection) + fg(t.Cyan) + " " + label + " " + ansi.Reset
	}

	var labels []string
	for i := range images {
		labels = append(labels, fmt.Sprintf("image %d", i+1))
	}
	for _, path := range a.pendingFiles {
		labels = append(labels, filepath.Base(path))
	}

	prefix := cellIndent + " "
	line := prefix
	shown := 0
	for i, label := range labels {
		remaining := len(labels) - i - 1
		candidate := chip(ansi.Truncate(label, 24, "…"))
		extra := ""
		if line != prefix {
			extra = " "
		}
		more := ""
		if remaining > 0 {
			more = " " + dim(fmt.Sprintf("+%d", remaining))
		}
		if ansi.Width(line+extra+candidate+more) > width {
			break
		}
		line += extra + candidate
		shown++
	}
	if shown < len(labels) {
		count := dim(fmt.Sprintf("+%d more", len(labels)-shown))
		if ansi.Width(line+" "+count) <= width {
			line += " " + count
		}
	}
	return []string{ansi.Truncate(line, width, "…")}
}
