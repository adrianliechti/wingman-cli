package server

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var excludedDirs = map[string]bool{
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".next":        true,
	".cache":       true,
}

var extToLanguage = map[string]string{
	".go":         "go",
	".js":         "javascript",
	".ts":         "typescript",
	".tsx":        "typescript",
	".jsx":        "javascript",
	".py":         "python",
	".rs":         "rust",
	".java":       "java",
	".kt":         "kotlin",
	".rb":         "ruby",
	".php":        "php",
	".c":          "c",
	".cpp":        "cpp",
	".h":          "c",
	".hpp":        "cpp",
	".cs":         "csharp",
	".swift":      "swift",
	".sh":         "bash",
	".bash":       "bash",
	".zsh":        "bash",
	".yaml":       "yaml",
	".yml":        "yaml",
	".json":       "json",
	".xml":        "xml",
	".html":       "html",
	".htm":        "html",
	".css":        "css",
	".scss":       "scss",
	".sql":        "sql",
	".md":         "markdown",
	".toml":       "toml",
	".ini":        "ini",
	".cfg":        "ini",
	".dockerfile": "dockerfile",
	".proto":      "protobuf",
	".lua":        "lua",
	".r":          "r",
	".dart":       "dart",
	".zig":        "zig",
	".ex":         "elixir",
	".exs":        "elixir",
	".erl":        "erlang",
	".hs":         "haskell",
	".ml":         "ocaml",
	".tf":         "hcl",
	".vue":        "vue",
	".svelte":     "svelte",
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	dirRel, ok := s.workspaceDirRel(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	dirPath := filepath.ToSlash(dirRel)

	fsys := s.workspace.Root.FS()

	entries, err := fs.ReadDir(fsys, dirPath)
	if err != nil {
		http.Error(w, "directory not found", http.StatusNotFound)
		return
	}

	var files []FileEntry

	for _, entry := range entries {
		name := entry.Name()

		if strings.HasPrefix(name, ".") {
			continue
		}

		if entry.IsDir() && excludedDirs[name] {
			continue
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

func (s *Server) handleFilesSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	const limit = 50

	fsys := s.workspace.Root.FS()

	type hit struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}

	results := make([]hit, 0, limit)

	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		name := d.Name()

		if d.IsDir() {
			if p == "." {
				return nil
			}
			if strings.HasPrefix(name, ".") || excludedDirs[name] {
				return fs.SkipDir
			}
			return nil
		}

		if strings.HasPrefix(name, ".") {
			return nil
		}

		if q != "" && !strings.Contains(strings.ToLower(p), q) {
			return nil
		}

		results = append(results, hit{Path: p, Name: name})

		if len(results) >= limit {
			return fs.SkipAll
		}

		return nil
	})

	writeJSON(w, results)
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.resolveExistingRegularFile(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	filePath := filepath.ToSlash(rel)
	data, err := s.workspace.Root.ReadFile(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	size := int64(len(data))

	if strings.EqualFold(filepath.Ext(filePath), ".svg") {
		writeJSON(w, FileContent{
			Path:   filePath,
			Binary: true,
			Mime:   "image/svg+xml",
			Size:   size,
		})
		return
	}

	if isBinary(data) {
		head := data
		if len(head) > 512 {
			head = head[:512]
		}
		mime := http.DetectContentType(head)
		writeJSON(w, FileContent{
			Path:   filePath,
			Binary: true,
			Mime:   mime,
			Size:   size,
		})
		return
	}

	writeJSON(w, FileContent{
		Path:     filePath,
		Content:  string(data),
		Language: languageForPath(filePath),
		Size:     size,
	})
}

func languageForPath(filePath string) string {
	if lang := extToLanguage[strings.ToLower(filepath.Ext(filePath))]; lang != "" {
		return lang
	}
	switch strings.ToLower(filepath.Base(filePath)) {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	case "cmakelists.txt":
		return "cmake"
	}
	return ""
}

func isBinary(data []byte) bool {
	const sniff = 8192
	head := data
	if len(head) > sniff {
		head = head[:sniff]
	}
	return bytes.IndexByte(head, 0) >= 0
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	rel, ok := s.resolveExistingRegularFile(w, body.Path)
	if !ok {
		return
	}
	info, _ := s.workspace.Root.Lstat(rel)

	if err := s.workspace.Root.WriteFile(rel, []byte(body.Content), info.Mode().Perm()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.flushFiles()
	if s.workspace.HasLSP() {
		s.broadcast(Frame{Type: EvtDiagnosticsChanged})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) workspaceRel(p string) (string, bool) {
	if p == "" {
		return "", false
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || path.IsAbs(cleaned) {
		return "", false
	}
	return filepath.FromSlash(cleaned), true
}

func (s *Server) workspaceDirRel(p string) (string, bool) {
	if p == "" || p == "." {
		return ".", true
	}
	return s.workspaceRel(p)
}

func (s *Server) resolveExistingRegularFile(w http.ResponseWriter, p string) (string, bool) {
	rel, ok := s.workspaceRel(p)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return "", false
	}
	info, err := s.workspace.Root.Lstat(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return "", false
	}
	if info.IsDir() {
		http.Error(w, "path is a directory", http.StatusBadRequest)
		return "", false
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return "", false
	}
	return rel, true
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.workspaceRel(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	if err := s.workspace.Root.RemoveAll(rel); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.flushFiles()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	fromRel, ok := s.workspaceRel(body.From)
	if !ok {
		http.Error(w, "invalid from path", http.StatusBadRequest)
		return
	}
	toRel, ok := s.workspaceRel(body.To)
	if !ok {
		http.Error(w, "invalid to path", http.StatusBadRequest)
		return
	}

	if _, err := s.workspace.Root.Lstat(toRel); err == nil {
		http.Error(w, "destination already exists", http.StatusConflict)
		return
	}

	if err := s.workspace.Root.Rename(fromRel, toRel); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.flushFiles()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	fromRel, ok := s.workspaceRel(body.From)
	if !ok {
		http.Error(w, "invalid from path", http.StatusBadRequest)
		return
	}
	toRel, ok := s.workspaceRel(body.To)
	if !ok {
		http.Error(w, "invalid to path", http.StatusBadRequest)
		return
	}

	root := s.workspace.Root

	if _, err := root.Lstat(toRel); err == nil {
		http.Error(w, "destination already exists", http.StatusConflict)
		return
	}

	info, err := root.Lstat(fromRel)
	if err != nil {
		http.Error(w, "source not found", http.StatusNotFound)
		return
	}

	if err := copyPathInRoot(root, fromRel, toRel, info); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.flushFiles()
	w.WriteHeader(http.StatusNoContent)
}

func copyPathInRoot(root *os.Root, src, dst string, info os.FileInfo) error {
	if info.IsDir() {
		if err := root.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(src))
		if err != nil {
			return err
		}
		for _, e := range entries {
			ei, err := e.Info()
			if err != nil {
				return err
			}
			if err := copyPathInRoot(root, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), ei); err != nil {
				return err
			}
		}
		return nil
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	in, err := root.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.resolveExistingRegularFile(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}

	root := s.workspace.Root
	f, err := root.Open(rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	name := filepath.Base(rel)
	disposition := "attachment; filename=\"" + strings.ReplaceAll(name, "\"", "") + "\"; filename*=UTF-8''" + url.PathEscape(name)
	w.Header().Set("Content-Disposition", disposition)
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// handleFilePreview redirects an HTML document to the isolated preview server.
// The preview origin is rooted at the document's directory so both relative
// and root-relative asset URLs behave like they do on a normal static server.
func (s *Server) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.resolveExistingRegularFile(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, s.preview.startURL(rel, r.Host), http.StatusTemporaryRedirect)
}
