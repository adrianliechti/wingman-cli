package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/go-extract"
	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

// MaxDocumentBytes bounds the source file loaded into memory for document
// conversion. Format-specific extractors apply their own, tighter limits to
// archive contents and attachments.
const MaxDocumentBytes = 128 * 1024 * 1024

func documentPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf", ".rtf",
		".docx", ".docm", ".dotx", ".dotm",
		".xlsx", ".xlsm", ".xltx", ".xltm",
		".pptx", ".pptm", ".ppsx", ".ppsm", ".potx", ".potm",
		".eml", ".msg":
		return true
	default:
		return false
	}
}

func documentData(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.Contains(head, []byte("%PDF-")) ||
		bytes.HasPrefix(head, []byte{'P', 'K', 0x03, 0x04}) ||
		bytes.HasPrefix(head, []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1})
}

func readDocument(
	ctx context.Context,
	inputTarget fileTarget,
	inputPath string,
	data []byte,
	startLine, limit int,
	freshness *Freshness,
) (tool.Result, bool, error) {
	explicit := documentPath(inputPath)
	if !explicit && !documentData(data) {
		return tool.Result{}, false, nil
	}

	doc, err := extract.Extract(ctx, extract.Input{
		Name: filepath.Base(inputPath),
		Data: data,
	}, extract.Options{DiscardAttachmentData: true})
	if err != nil {
		if errors.Is(err, extract.ErrUnsupportedFormat) {
			if !explicit {
				return tool.Result{}, false, nil
			}
			return tool.Result{}, true, fmt.Errorf("cannot read document %q: unsupported format (supported: PDF, DOCX, XLSX, PPTX, HTML, EML, MSG)", inputPath)
		}
		return tool.Result{}, true, fmt.Errorf("read document %q: %w", inputPath, err)
	}
	freshness.record(ctx, inputTarget)

	metadata := map[string]any{
		"file_path": inputPath,
		"format":    string(doc.Format),
	}
	if doc.MediaType != "" {
		metadata["media_type"] = doc.MediaType
	}
	if len(doc.Metadata) > 0 {
		metadata["document"] = doc.Metadata
	}
	if len(doc.Attachments) > 0 {
		metadata["attachment_count"] = len(doc.Attachments)
	}

	content := formatRead([]byte(doc.Markdown), startLine, limit)
	if warning := documentExtractionWarning(doc); warning != "" {
		content += "\n\n" + warning
	}
	return tool.Result{Content: content, Metadata: metadata}, true, nil
}

func documentExtractionWarning(doc *extract.Document) string {
	if doc == nil || doc.Format != extract.FormatPDF {
		return ""
	}
	pages := doc.Metadata["pages_needing_ocr"]
	if pages == "" {
		return ""
	}
	return fmt.Sprintf("<system-reminder>PDF pages %s need OCR or visual inspection; their extracted text may be incomplete.</system-reminder>", pages)
}
