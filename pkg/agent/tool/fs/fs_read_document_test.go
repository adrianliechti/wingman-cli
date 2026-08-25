package fs

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
)

func TestReadToolReadsDocumentAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeTestDOCX(t, filepath.Join(dir, "report.docx"), []string{"Alpha", "Beta", "Gamma"})

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	tl := ReadTool(root)
	result, err := tl.Execute(context.Background(), map[string]any{"file_path": "report.docx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "1\tAlpha") || !strings.Contains(result.Content, "3\tBeta") {
		t.Fatalf("content = %q", result.Content)
	}
	if result.Metadata["format"] != "docx" || result.Metadata["file_path"] != "report.docx" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	if got := tl.Effect(nil); got != tool.EffectReadOnly {
		t.Fatalf("read effect = %q, want %q", got, tool.EffectReadOnly)
	}

	window, err := tl.Execute(context.Background(), map[string]any{
		"file_path": "report.docx",
		"offset":    3,
		"limit":     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(window.Content, "3\tBeta") || strings.Contains(window.Content, "Alpha") {
		t.Fatalf("window = %q", window.Content)
	}
}

func TestReadToolReadsPDFAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.pdf"), buildTestPDF("PDF report text"), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	result, err := ReadTool(root).Execute(context.Background(), map[string]any{
		"file_path": "report.pdf",
		"offset":    1,
		"limit":     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "1\tPDF report text") {
		t.Fatalf("content = %q", result.Content)
	}
	if result.Metadata["format"] != "pdf" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestReadToolReadsRTFAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.rtf"), []byte(`{\rtf1\ansi RTF report\par Second line}`), 0o644); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	result, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "report.rtf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "1\tRTF report") || !strings.Contains(result.Content, "2\tSecond line") {
		t.Fatalf("content = %q", result.Content)
	}
	if result.Metadata["format"] != "rtf" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestReadToolReadsOfficeTemplateAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	writeTestDOCX(t, filepath.Join(dir, "report.dotx"), []string{"Template text"})

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	result, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "report.dotx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "1\tTemplate text") {
		t.Fatalf("content = %q", result.Content)
	}
	if result.Metadata["format"] != "docx" || result.Metadata["media_type"] != "application/vnd.openxmlformats-officedocument.wordprocessingml.template" {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestReadToolCapsDocumentOutput(t *testing.T) {
	dir := t.TempDir()
	writeTestDOCX(t, filepath.Join(dir, "large.docx"), []string{strings.Repeat("x", DefaultMaxBytes+1024)})

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	result, err := ReadTool(root).Execute(context.Background(), map[string]any{"file_path": "large.docx"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "48KB cap reached") {
		t.Fatalf("result was not capped: %d bytes", len(result.Content))
	}
	if len(result.Content) > DefaultMaxBytes+256 {
		t.Fatalf("capped result is unexpectedly large: %d bytes", len(result.Content))
	}
}

func writeTestDOCX(t *testing.T, path string, paragraphs []string) {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	parts := map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/></Types>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rDoc" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
	}
	var document strings.Builder
	document.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, paragraph := range paragraphs {
		document.WriteString(`<w:p><w:r><w:t>`)
		document.WriteString(paragraph)
		document.WriteString(`</w:t></w:r></w:p>`)
	}
	document.WriteString(`</w:body></w:document>`)
	parts["word/document.xml"] = document.String()

	for name, content := range parts {
		part, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildTestPDF(text string) []byte {
	content := fmt.Sprintf("BT\n/F1 18 Tf\n72 720 Td\n(%s) Tj\nET\n", strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text))
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return pdf.Bytes()
}
