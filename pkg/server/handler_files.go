package server

import (
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// Language mapping for syntax highlighting hints
var extToLanguage = map[string]string{
	".go":    "go",
	".js":    "javascript",
	".ts":    "typescript",
	".tsx":   "tsx",
	".jsx":   "jsx",
	".py":    "python",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".rb":    "ruby",
	".php":   "php",
	".c":     "c",
	".cpp":   "cpp",
	".h":     "c",
	".hpp":   "cpp",
	".cs":    "csharp",
	".swift": "swift",
	".sh":    "bash",
	".bash":  "bash",
	".zsh":   "bash",
	".yaml":  "yaml",
	".yml":   "yaml",
	".json":  "json",
	".xml":   "xml",
	".html":  "html",
	".css":   "css",
	".scss":  "scss",
	".sql":   "sql",
	".md":    "markdown",
	".toml":  "toml",
	".ini":   "ini",
	".cfg":   "ini",
	".dockerfile": "dockerfile",
	".proto": "protobuf",
	".lua":   "lua",
	".r":     "r",
	".dart":  "dart",
	".zig":   "zig",
	".ex":    "elixir",
	".exs":   "elixir",
	".erl":   "erlang",
	".hs":    "haskell",
	".ml":    "ocaml",
	".tf":    "hcl",
	".vue":   "vue",
	".svelte": "svelte",
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "."
	}

	// Sanitize path
	dirPath = path.Clean(dirPath)
	if strings.HasPrefix(dirPath, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	fsys := s.agent.Root.FS()

	entries, err := fs.ReadDir(fsys, dirPath)
	if err != nil {
		http.Error(w, "directory not found", http.StatusNotFound)
		return
	}

	var files []FileEntry

	for _, entry := range entries {
		name := entry.Name()

		// Skip hidden files/dirs
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Skip common large directories
		if entry.IsDir() {
			if name == "node_modules" || name == "__pycache__" || name == ".venv" || name == "vendor" {
				continue
			}
		}

		entryPath := path.Join(dirPath, name)
		if dirPath == "." {
			entryPath = name
		}

		var size int64
		if info, err := entry.Info(); err == nil {
			size = info.Size()
		}

		files = append(files, FileEntry{
			Name:  name,
			Path:  entryPath,
			IsDir: entry.IsDir(),
			Size:  size,
		})
	}

	if files == nil {
		files = []FileEntry{}
	}

	writeJSON(w, files)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	// Sanitize
	filePath = path.Clean(filePath)
	if strings.HasPrefix(filePath, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	fsys := s.agent.Root.FS()

	data, err := fs.ReadFile(fsys, filePath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	lang := extToLanguage[ext]

	// Special cases
	base := strings.ToLower(filepath.Base(filePath))
	if lang == "" {
		switch base {
		case "dockerfile":
			lang = "dockerfile"
		case "makefile":
			lang = "makefile"
		case "cmakelists.txt":
			lang = "cmake"
		}
	}

	writeJSON(w, FileContent{
		Path:     filePath,
		Content:  string(data),
		Language: lang,
	})
}
