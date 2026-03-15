package prompt

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed instructions.txt
var Instructions string

//go:embed planning.txt
var Planning string

//go:embed compaction.txt
var Compaction string

//go:embed review.txt
var Review string

//go:embed lsp.txt
var LSP string

func Render(tmpl string, data any) (string, error) {
	t, err := template.New("prompt").Parse(tmpl)

	if err != nil {
		return "", err
	}

	var buf bytes.Buffer

	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}
