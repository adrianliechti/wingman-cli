package fs

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// Anthropic-family backends cap images at 5MB, the strictest of the
// supported providers.
const maxImageBytes = 5 * 1024 * 1024

var imageMimeTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

func ImageTool(root *os.Root, allowedReadRoots ...string) tool.Tool {
	return tool.Tool{
		Name:   "view_image",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			"View an image file: it is attached to the conversation so you can actually see it.",
			"- Use for screenshots, UI mockups, diagrams, and other visual assets — e.g. take a screenshot via shell, then view it to verify a UI change.",
			fmt.Sprintf("- Supported: PNG, JPEG, GIF, WebP. Max %dMB and %dpx per dimension.", maxImageBytes/(1024*1024), tool.MaxImageDimension),
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "Path to the image file."},
			},
			"required":             []string{"file_path"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			pathArg, ok := args["file_path"].(string)

			if !ok || pathArg == "" {
				return tool.Result{}, fmt.Errorf("file_path is required")
			}

			target, err := resolveFileTarget(pathArg, root.Name(), allowedReadRoots, "view image")
			if err != nil {
				return tool.Result{}, err
			}

			info, err := statFileTarget(root, target)
			if err != nil {
				return tool.Result{}, fmt.Errorf("stat file %q: %w", pathArg, err)
			}
			if info.IsDir() {
				return tool.Result{}, fmt.Errorf("cannot view image: path %q is a directory", pathArg)
			}
			if !info.Mode().IsRegular() {
				return tool.Result{}, fmt.Errorf("cannot view image: path %q is not a regular file", pathArg)
			}
			if info.Size() > maxImageBytes {
				return tool.Result{}, fmt.Errorf("cannot view image: %q is %.1fMB (max %dMB)", pathArg, float64(info.Size())/(1024*1024), maxImageBytes/(1024*1024))
			}

			content, err := readFileTarget(root, target)
			if err != nil {
				return tool.Result{}, fmt.Errorf("read file %q: %w", pathArg, err)
			}

			mime := http.DetectContentType(content)
			if !imageMimeTypes[mime] {
				return tool.Result{}, fmt.Errorf("cannot view image: %q is not a supported image format (detected %s)", pathArg, mime)
			}
			width, height, err := tool.ImageDimensions(content, mime)
			if err != nil {
				return tool.Result{}, fmt.Errorf("cannot view image %q: %w", pathArg, err)
			}
			if width > tool.MaxImageDimension || height > tool.MaxImageDimension {
				return tool.Result{}, fmt.Errorf("cannot view image: %q is %dx%d (max %dpx per dimension); resize or crop it first", pathArg, width, height, tool.MaxImageDimension)
			}

			return tool.Text("data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(content)), nil
		},
	}
}
