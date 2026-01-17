package fs

import (
	"fmt"
	"strings"

	"github.com/adrianliechti/wingman-cli/pkg/tool"
)

func ReadTool() tool.Tool {
	return tool.Tool{
		Name: "read",

		Description: fmt.Sprintf(
			"Read the contents of a file. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files.",
			DefaultMaxLines,
			DefaultMaxBytes/1024,
		),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Path to the file to read"},
				"offset": map[string]any{"type": "integer", "description": "Line number to start reading from (1-based)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to read"},
			},
			"required": []string{"path"},
		},

		Execute: func(env *tool.Environment, args map[string]any) (string, error) {
			pathArg, ok := args["path"].(string)
			if !ok || pathArg == "" {
				return "", fmt.Errorf("path is required")
			}

			var offset int
			if o, ok := args["offset"].(float64); ok {
				offset = int(o)
			}

			var limit int
			if l, ok := args["limit"].(float64); ok {
				limit = int(l)
			}

			content, err := env.Root.ReadFile(pathArg)
			if err != nil {
				return "", fmt.Errorf("file not found: %s", pathArg)
			}

			textContent := string(content)
			allLines := strings.Split(textContent, "\n")
			totalFileLines := len(allLines)

			startLine := 0
			if offset > 0 {
				startLine = offset - 1
			}

			if startLine >= len(allLines) {
				return "", fmt.Errorf("offset %d is beyond end of file (%d lines total)", offset, totalFileLines)
			}

			var selectedLines []string
			var userLimitedLines int

			if limit > 0 {
				endLine := startLine + limit
				if endLine > len(allLines) {
					endLine = len(allLines)
				}
				selectedLines = allLines[startLine:endLine]
				userLimitedLines = endLine - startLine
			} else {
				selectedLines = allLines[startLine:]
			}

			selectedContent := strings.Join(selectedLines, "\n")
			truncatedContent, truncatedByLines, truncatedByBytes := truncateHead(selectedContent)

			startLineDisplay := startLine + 1

			if truncatedByLines || truncatedByBytes {
				outputLines := len(strings.Split(truncatedContent, "\n"))
				endLineDisplay := startLineDisplay + outputLines - 1
				nextOffset := endLineDisplay + 1

				var notice string
				if truncatedByLines {
					notice = fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue]",
						startLineDisplay, endLineDisplay, totalFileLines, nextOffset)
				} else {
					notice = fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%dKB limit). Use offset=%d to continue]",
						startLineDisplay, endLineDisplay, totalFileLines, DefaultMaxBytes/1024, nextOffset)
				}

				return truncatedContent + notice, nil
			}

			if userLimitedLines > 0 && startLine+userLimitedLines < len(allLines) {
				remaining := len(allLines) - (startLine + userLimitedLines)
				nextOffset := startLine + userLimitedLines + 1

				return truncatedContent + fmt.Sprintf("\n\n[%d more lines in file. Use offset=%d to continue]",
					remaining, nextOffset), nil
			}

			return truncatedContent, nil
		},
	}
}
