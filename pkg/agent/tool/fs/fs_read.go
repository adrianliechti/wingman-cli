package fs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func ReadTool(root *os.Root, allowedReadRoots ...string) tool.Tool {
	return readTool(root, nil, nil, 0, allowedReadRoots...)
}

func readTool(root *os.Root, tracker *contentTracker, freshness *Freshness, maxFileBytes int64, allowedReadRoots ...string) tool.Tool {
	if maxFileBytes == 0 {
		maxFileBytes = MaxReadFileBytes
	}

	return tool.Tool{
		Name:   "read",
		Effect: tool.StaticEffect(tool.EffectReadOnly),

		Description: strings.Join([]string{
			fmt.Sprintf("Reads a file from the local filesystem. Results use cat -n format with 1-based line numbers. By default reads the first %d lines; output is capped at %dKB, with a trailing notice telling you which offset to continue from.", DefaultMaxLines, DefaultMaxBytes/1024),
			"- PDF, RTF, supported Word, Excel, and PowerPoint documents (including macro-enabled files, templates, and slide shows), EML, and Outlook MSG files are converted to Markdown. `offset` and `limit` select lines from that Markdown just like any text file.",
			"- Use `offset` and `limit` for long files or known ranges. `offset` is a 1-based start line, not a result skip count.",
			"- Reads files only, not directories. Use `glob` to list files in a directory.",
			"- Other binary files are rejected. Use `view_image` for image files. SVG and HTML files are treated as text.",
		}, "\n"),

		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{"type": "string", "description": "The absolute path to the file to read."},
				"offset":    map[string]any{"type": "integer", "description": "1-based line number to start reading from, including for converted documents. Only provide for large files or known ranges. Defaults to 1."},
				"limit":     map[string]any{"type": "integer", "description": "Positive number of lines to read, including from converted documents. Only provide for large files or known ranges."},
			},
			"required":             []string{"file_path"},
			"additionalProperties": false,
		},

		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			pathArg, ok := args["file_path"].(string)

			if !ok || pathArg == "" {
				return tool.Result{}, fmt.Errorf("file_path is required")
			}

			workingDir := root.Name()

			limit := 0
			if v, present, err := tool.PositiveIntArg(args, "limit"); present {
				if err != nil {
					return tool.Result{}, err
				}
				limit = v
			}

			startLine := 1
			if v, present, err := tool.PositiveIntArg(args, "offset"); present {
				if err != nil {
					return tool.Result{}, fmt.Errorf("offset must be a positive 1-based integer")
				}
				startLine = v
			}

			target, err := resolveFileTarget(pathArg, workingDir, allowedReadRoots, "read file")
			if err != nil {
				return tool.Result{}, err
			}

			info, err := statFileTarget(root, target)
			if err != nil {
				return tool.Result{}, fmt.Errorf("stat file %q: %w", pathArg, err)
			}
			if info.IsDir() {
				return tool.Result{}, fmt.Errorf("cannot read file: path %q is a directory; use glob to find files inside it", pathArg)
			}
			if !info.Mode().IsRegular() {
				return tool.Result{}, fmt.Errorf("cannot read file: path %q is not a regular file", pathArg)
			}
			documentHint := documentPath(pathArg)
			if documentHint && info.Size() > MaxDocumentBytes {
				return tool.Result{}, fmt.Errorf("cannot read document: %q is %.1fMB (max %dMB)", pathArg, float64(info.Size())/(1024*1024), MaxDocumentBytes/(1024*1024))
			}
			if !documentHint && maxFileBytes > 0 && info.Size() > maxFileBytes {
				output, err := readFileWindow(root, target, pathArg, info.Size(), startLine, limit)
				if err != nil {
					return tool.Result{}, err
				}
				freshness.record(ctx, target)
				return tool.Text(output), nil
			}

			content, err := readFileTarget(root, target)
			if err != nil {
				return tool.Result{}, fmt.Errorf("read file %q: %w", pathArg, err)
			}

			if len(content) > MaxDocumentBytes && (documentHint || documentData(content)) {
				return tool.Result{}, fmt.Errorf("cannot read document: %q grew beyond the %dMB limit while being read", pathArg, MaxDocumentBytes/(1024*1024))
			}

			if result, handled, err := readDocument(ctx, target, pathArg, content, startLine, limit, freshness); handled {
				return result, err
			}

			if isBinaryContent(content) {
				return tool.Result{}, fmt.Errorf("cannot read %s: file appears to be binary. Use the shell tool with an appropriate viewer if you really need to inspect it", pathArg)
			}

			tracker.record(content)
			freshness.record(ctx, target)

			return tool.Text(formatRead(content, startLine, limit)), nil
		},
	}
}

func formatRead(content []byte, startLine, limit int) string {
	if len(content) == 0 {
		return "<system-reminder>Warning: the file exists but the contents are empty.</system-reminder>"
	}

	_, text := stripBom(string(content))
	lines := strings.Split(normalizeToLF(text), "\n")
	total := len(lines)
	offset := startLine - 1

	if offset >= total {
		return fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>", startLine, total)
	}

	maxLines := DefaultMaxLines
	if limit > 0 {
		maxLines = limit
	}

	end := min(total, offset+maxLines)

	var numbered []string

	for i, line := range lines[offset:end] {
		lineNum := offset + i + 1
		numbered = append(numbered, fmt.Sprintf("%d\t%s", lineNum, line))
	}

	selected := strings.Join(numbered, "\n")
	output, bytesTruncated := truncateReadOutput(selected)

	outputLines := 0
	if output != "" {
		outputLines = strings.Count(output, "\n") + 1
	}
	endLine := offset + outputLines

	if bytesTruncated || end < total {
		notice := fmt.Sprintf("Showing lines %d-%d of %d", startLine, endLine, total)
		if bytesTruncated {
			notice += fmt.Sprintf("; %dKB cap reached", DefaultMaxBytes/1024)
		}
		if endLine < total {
			notice += fmt.Sprintf("; use offset=%d to continue", endLine+1)
		}
		return fmt.Sprintf("%s\n\n[%s]", output, notice)
	}

	return output
}

const maxReadLineBytes = 512 * 1024

// readFileWindow serves files too large to load whole: it streams the file and
// keeps only the requested line window in memory.
func readFileWindow(root *os.Root, target fileTarget, pathArg string, fileSize int64, startLine, limit int) (string, error) {
	f, err := openFileTarget(root, target)
	if err != nil {
		return "", fmt.Errorf("read file %q: %w", pathArg, err)
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)

	if head, err := reader.Peek(512); err == nil && isBinaryContent(head) {
		return "", fmt.Errorf("cannot read %s: file appears to be binary. Use the shell tool with an appropriate viewer if you really need to inspect it", pathArg)
	}

	maxLines := DefaultMaxLines
	if limit > 0 {
		maxLines = limit
	}

	var numbered []string
	size := 0
	lineNum := 0
	sawEOF := false

	for {
		line, tooLong, err := readLimitedLine(reader)
		if tooLong {
			return "", fmt.Errorf("cannot read %s: line %d is longer than %dKB; use grep or the shell tool to inspect this file", pathArg, lineNum+1, maxReadLineBytes/1024)
		}
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read file %q: %w", pathArg, err)
		}
		sawEOF = err == io.EOF

		lineNum++
		if lineNum == 1 {
			_, line = stripBom(line)
		}

		if lineNum >= startLine {
			text := strings.TrimSuffix(line, "\n")
			text = strings.TrimSuffix(text, "\r")
			numbered = append(numbered, fmt.Sprintf("%d\t%s", lineNum, text))
			size += len(text)
		}

		if sawEOF || len(numbered) >= maxLines || size >= DefaultMaxBytes {
			break
		}
	}

	if len(numbered) == 0 {
		return fmt.Sprintf("<system-reminder>Warning: the file exists but is shorter than the provided offset (%d). The file has %d lines.</system-reminder>", startLine, lineNum), nil
	}

	output, bytesTruncated := truncateReadOutput(strings.Join(numbered, "\n"))
	outputLines := strings.Count(output, "\n") + 1
	endLine := startLine + outputLines - 1
	notice := fmt.Sprintf("Showing lines %d-%d of a %.1fMB file (too large to read fully)", startLine, endLine, float64(fileSize)/(1024*1024))
	if bytesTruncated {
		notice += fmt.Sprintf("; %dKB cap reached", DefaultMaxBytes/1024)
	}
	if !sawEOF {
		notice += fmt.Sprintf("; use offset=%d to continue", endLine+1)
	}
	return fmt.Sprintf("%s\n\n[%s]", output, notice), nil
}

// readLimitedLine reads one newline-terminated line while capping how much of
// an unbroken line is held in memory.
func readLimitedLine(r *bufio.Reader) (string, bool, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxReadLineBytes {
			return "", true, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return string(line), false, err
	}
}

func truncateReadOutput(content string) (string, bool) {
	if len(content) <= DefaultMaxBytes {
		return content, false
	}

	cut := strings.LastIndex(content[:DefaultMaxBytes], "\n")
	if cut <= 0 {
		return content[:DefaultMaxBytes], true
	}

	return content[:cut], true
}
