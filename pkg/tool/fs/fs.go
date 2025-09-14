package fs

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func New(root string) (*FS, error) {
	root, err := filepath.Abs(root)

	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}

	fs := &FS{
		root: root,
	}

	return fs, nil
}

var (
	_ tool.Provider = (*FS)(nil)
)

type FS struct {
	root string
}

type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`

	Size      int64     `json:"size"`
	Timestamp time.Time `json:"timestamp"`
}

func (fs *FS) Tools(ctx context.Context) ([]tool.Tool, error) {
	return []tool.Tool{
		{
			Name:        "find_text",
			Description: "find files containing text recursively at path",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},

					"pattern": {
						Type:        "string",
						Description: "regular expression pattern (RE2 syntax)",
					},
				},

				Required: []string{"path", "pattern"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)
				pattern := params["pattern"].(string)

				return fs.FindText(path, pattern)
			},
		},
		{
			Name:        "list_dir",
			Description: "list files and directories recursively at path",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},
				},

				Required: []string{"path"},
			},
			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)
				return fs.ListDir(path)
			},
		},
		{
			Name:        "read_file",
			Description: "read the (text) content of a file at path",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},
				},

				Required: []string{"path"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)
				return fs.ReadFile(path)
			},
		},
		{
			Name:        "create_file",
			Description: "create or overwrite file at path with content (text)",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},

					"content": {
						Type: "string",
					},
				},

				Required: []string{"path", "content"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)
				data := params["content"].(string)

				if err := fs.CreateFile(path, data); err != nil {
					return nil, err
				}

				return "file created", nil
			},
		},
		{
			Name:        "delete_file",
			Description: "delete a file at path and all empty parent directories",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},
				},

				Required: []string{"path"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)

				if err := fs.DeleteFile(path); err != nil {
					return nil, err
				}

				return "file deleted", nil
			},
		},
		{
			Name:        "create_dir",
			Description: "create a directory at path and all missing parent directories",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},
				},

				Required: []string{"path"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)

				if err := fs.CreateDir(path); err != nil {
					return nil, err
				}

				return "directory created", nil
			},
		},
		{
			Name:        "delete_dir",
			Description: "delete a dir at path and all child files and directories",

			Schema: &tool.Schema{
				Type: "object",

				Properties: map[string]*tool.Schema{
					"path": {
						Type: "string",
					},
				},

				Required: []string{"path"},
			},

			ToolHandler: func(ctx context.Context, params map[string]any) (any, error) {
				path := params["path"].(string)

				if err := fs.DeleteDir(path); err != nil {
					return nil, err
				}

				return "directory deleted", nil
			},
		},
	}, nil
}

func (fs *FS) FindText(path, pattern string) ([]FileInfo, error) {
	path = filepath.Join(fs.root, path)

	var result []FileInfo

	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)

		if err != nil {
			return err
		}

		matches, err := regexp.Match(pattern, data)

		if err != nil || !matches {
			return nil
		}

		path, _ = filepath.Rel(fs.root, path)

		if path == "." {
			return nil
		}

		result = append(result, FileInfo{
			Name: info.Name(),
			Path: filepath.ToSlash(path),

			Size:      info.Size(),
			Timestamp: info.ModTime(),
		})

		return nil
	})

	return result, nil
}

func (fs *FS) ListDir(path string) ([]FileInfo, error) {
	path = filepath.Join(fs.root, path)

	var result []FileInfo

	filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		path, _ = filepath.Rel(fs.root, path)

		if path == "." {
			return nil
		}

		file := FileInfo{
			Name: info.Name(),
			Path: filepath.ToSlash(path),

			Size:      info.Size(),
			Timestamp: info.ModTime(),
		}

		result = append(result, file)

		return nil
	})

	return result, nil
}

func (fs *FS) CreateFile(path, content string) error {
	path = filepath.Join(fs.root, path)

	os.MkdirAll(filepath.Dir(path), 0755)
	return os.WriteFile(path, []byte(content), 0644)
}

func (fs *FS) ReadFile(path string) (string, error) {
	path = fs.resolvePath(path)

	data, err := os.ReadFile(path)

	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (fs *FS) DeleteFile(path string) error {
	path = fs.resolvePath(path)

	os.Remove(path)

	for dir := filepath.Dir(path); dir != fs.root; dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			if !os.IsNotExist(err) && !os.IsExist(err) {
				continue
			}

			break
		}
	}

	return nil
}

func (fs *FS) CreateDir(path string) error {
	path = fs.resolvePath(path)
	return os.MkdirAll(path, 0755)
}

func (fs *FS) DeleteDir(path string) error {
	path = fs.resolvePath(path)
	return os.RemoveAll(path)
}

func (fs *FS) resolvePath(path string) string {
	return filepath.Join(fs.root, filepath.FromSlash(path))
}
