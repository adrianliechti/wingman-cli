package markdown

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"

	"github.com/adrianliechti/wingman-cli/pkg/theme"
)

// TviewRenderer renders goldmark AST to tview dynamic color format
type TviewRenderer struct {
	theme theme.Theme
}

// NewTviewRenderer creates a new tview renderer
func NewTviewRenderer() *TviewRenderer {
	return &TviewRenderer{
		theme: theme.Default,
	}
}

// RegisterFuncs implements renderer.NodeRenderer
func (r *TviewRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// Block nodes
	reg.Register(ast.KindDocument, r.renderDocument)
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
	reg.Register(ast.KindCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindList, r.renderList)
	reg.Register(ast.KindListItem, r.renderListItem)
	reg.Register(ast.KindThematicBreak, r.renderThematicBreak)
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)

	// Inline nodes
	reg.Register(ast.KindText, r.renderText)
	reg.Register(ast.KindString, r.renderString)
	reg.Register(ast.KindCodeSpan, r.renderCodeSpan)
	reg.Register(ast.KindEmphasis, r.renderEmphasis)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindRawHTML, r.renderRawHTML)

	// GFM extensions
	reg.Register(east.KindTable, r.renderTable)
	reg.Register(east.KindTableHeader, r.renderTableHeader)
	reg.Register(east.KindTableRow, r.renderTableRow)
	reg.Register(east.KindTableCell, r.renderTableCell)
	reg.Register(east.KindStrikethrough, r.renderStrikethrough)
	reg.Register(east.KindTaskCheckBox, r.renderTaskCheckBox)
}

func (r *TviewRenderer) renderDocument(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderHeading(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	if entering {
		// Add spacing before heading (unless it's the first element)
		if node.PreviousSibling() != nil {
			w.WriteString("\n")
		}
		color := r.theme.Blue
		if n.Level >= 2 {
			color = r.theme.Magenta
		}
		fmt.Fprintf(w, "[%s::b]", color)
	} else {
		w.WriteString("[-]\n")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderBlockquote(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.PreviousSibling() != nil {
			w.WriteString("\n")
		}
		fmt.Fprintf(w, "[%s]> [-]", r.theme.BrBlack)
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.PreviousSibling() != nil {
			w.WriteString("\n")
		}
		n := node.(*ast.CodeBlock)
		var code strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			code.Write(line.Value(source))
		}
		w.WriteString(formatCodeBlock(code.String(), "", r.theme))
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.PreviousSibling() != nil {
			w.WriteString("\n")
		}
		n := node.(*ast.FencedCodeBlock)
		lang := ""
		if n.Info != nil {
			lang = string(n.Info.Segment.Value(source))
			// Extract just the language, ignore any metadata after space
			if idx := strings.IndexByte(lang, ' '); idx >= 0 {
				lang = lang[:idx]
			}
		}
		var code strings.Builder
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			code.Write(line.Value(source))
		}
		w.WriteString(formatCodeBlock(code.String(), lang, r.theme))
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderParagraph(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderList(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		// Add spacing before list (unless it's the first element or nested)
		if node.PreviousSibling() != nil {
			if _, isListItem := node.Parent().(*ast.ListItem); !isListItem {
				w.WriteString("\n")
			}
		}
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderListItem(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.ListItem)
		parent := n.Parent().(*ast.List)

		// Calculate indent level
		indent := ""
		for p := parent.Parent(); p != nil; p = p.Parent() {
			if _, ok := p.(*ast.ListItem); ok {
				indent += "  "
			}
		}

		if parent.IsOrdered() {
			// Find index of this item
			idx := 1
			for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
				if c == node {
					break
				}
				idx++
			}
			fmt.Fprintf(w, "%s[%s]%d.[-] ", indent, r.theme.Yellow, parent.Start+idx-1)
		} else {
			fmt.Fprintf(w, "%s[%s]•[-] ", indent, r.theme.Yellow)
		}
	} else {
		w.WriteString("\n")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderThematicBreak(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.PreviousSibling() != nil {
			w.WriteString("\n")
		}
		fmt.Fprintf(w, "[%s]────────────────────────────────────────[-]\n", r.theme.BrBlack)
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.HTMLBlock)
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			w.WriteString(tview.Escape(string(line.Value(source))))
		}
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderText(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.Text)
		segment := n.Segment
		w.WriteString(tview.Escape(string(segment.Value(source))))
		if n.HardLineBreak() || n.SoftLineBreak() {
			w.WriteString("\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderString(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.String)
		w.WriteString(tview.Escape(string(n.Value)))
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderCodeSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		fmt.Fprintf(w, "[%s]", r.theme.Cyan)
	} else {
		w.WriteString("[-]")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderEmphasis(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Emphasis)
	if entering {
		if n.Level == 2 {
			w.WriteString("[::b]")
		} else {
			w.WriteString("[::i]")
		}
	} else {
		w.WriteString("[::-]")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	if entering {
		fmt.Fprintf(w, "[%s]", r.theme.Cyan)
	} else {
		fmt.Fprintf(w, "[-] [%s](%s)[-]", r.theme.BrBlack, tview.Escape(string(n.Destination)))
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.AutoLink)
	if entering {
		url := n.URL(source)
		fmt.Fprintf(w, "[%s]%s[-]", r.theme.Cyan, tview.Escape(string(url)))
	}
	return ast.WalkSkipChildren, nil
}

func (r *TviewRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Image)
	if entering {
		fmt.Fprintf(w, "[%s]🖼 [-]", r.theme.Yellow)
	} else {
		fmt.Fprintf(w, "[%s](%s)[-]", r.theme.BrBlack, tview.Escape(string(n.Destination)))
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderRawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.RawHTML)
		segments := n.Segments
		for i := 0; i < segments.Len(); i++ {
			segment := segments.At(i)
			w.WriteString(tview.Escape(string(segment.Value(source))))
		}
	}
	return ast.WalkContinue, nil
}

// GFM extension renderers

func (r *TviewRenderer) renderTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteString("\n")
		return ast.WalkContinue, nil
	}

	// Add spacing before table
	if node.PreviousSibling() != nil {
		w.WriteString("\n")
	}

	// Collect all rows and cells to calculate column widths
	table := node.(*east.Table)
	var rows [][]string
	var isHeader []bool

	for row := node.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		_, header := row.(*east.TableHeader)
		isHeader = append(isHeader, header)

		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			// Extract cell text
			var cellText strings.Builder
			for child := cell.FirstChild(); child != nil; child = child.NextSibling() {
				if text, ok := child.(*ast.Text); ok {
					cellText.Write(text.Segment.Value(source))
				} else if str, ok := child.(*ast.String); ok {
					cellText.Write(str.Value)
				} else {
					// For other nodes, try to get text content
					for c := child.FirstChild(); c != nil; c = c.NextSibling() {
						if t, ok := c.(*ast.Text); ok {
							cellText.Write(t.Segment.Value(source))
						}
					}
				}
			}
			cells = append(cells, cellText.String())
		}
		rows = append(rows, cells)
	}

	// Calculate max width for each column
	colWidths := make([]int, len(table.Alignments))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(colWidths) && len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}

	// Render with proper padding
	for rowIdx, row := range rows {
		if isHeader[rowIdx] {
			w.WriteString("[::b]")
		}
		for i, cell := range row {
			if i > 0 {
				fmt.Fprintf(w, " [%s]│[-] ", r.theme.BrBlack)
			}
			escaped := tview.Escape(cell)
			w.WriteString(escaped)
			// Pad to column width
			if i < len(colWidths) {
				padding := colWidths[i] - len(cell)
				for j := 0; j < padding; j++ {
					w.WriteString(" ")
				}
			}
		}
		if isHeader[rowIdx] {
			w.WriteString("[::-]")
		}
		w.WriteString("\n")
	}

	return ast.WalkSkipChildren, nil
}

func (r *TviewRenderer) renderTableHeader(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderTableRow(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderTableCell(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// Handled by renderTable
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderStrikethrough(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		fmt.Fprintf(w, "[%s]~~", r.theme.BrBlack)
	} else {
		w.WriteString("~~[-]")
	}
	return ast.WalkContinue, nil
}

func (r *TviewRenderer) renderTaskCheckBox(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*east.TaskCheckBox)
		if n.IsChecked {
			fmt.Fprintf(w, "[%s]☑[-] ", r.theme.Green)
		} else {
			fmt.Fprintf(w, "[%s]☐[-] ", r.theme.BrBlack)
		}
	}
	return ast.WalkContinue, nil
}
