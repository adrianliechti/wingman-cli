package graph

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Location is an LSP-style position: zero-based line and UTF-16 column in a
// slash-separated workspace-relative file.
type Location struct {
	File string
	Line int
	Col  int
}

type SymRange struct {
	StartLine int
	StartCol  int
	EndLine   int
	EndCol    int
}

type Symbol struct {
	Name      string
	Kind      Kind
	Range     SymRange
	NameRange SymRange
	Children  []*Symbol
}

var navExtractors = sync.Pool{New: func() any {
	return &navExtractor{
		extractor:    newExtractor(),
		highlighters: make(map[string]*ts.Highlighter),
	}
}}

type navExtractor struct {
	*extractor
	highlighters map[string]*ts.Highlighter
}

// FileSymbols extracts a nested outline from a single buffer without touching
// the index, so unsaved editor content stays accurate.
func FileSymbols(filename string, src []byte) []*Symbol {
	entry := grammars.DetectLanguage(path.Base(filepath.ToSlash(filename)))
	if entry == nil || len(src) == 0 {
		return nil
	}

	ex := navExtractors.Get().(*navExtractor)
	defer navExtractors.Put(ex)

	tagger := ex.tagger(entry)
	if tagger == nil {
		return nil
	}
	tree, err := ex.parser(entry).Parse(src)
	if err != nil {
		return nil
	}
	defer tree.Release()

	li := newLineIndex(src)
	rangeAt := func(start, end uint32) SymRange {
		sl, sc := li.lspPos(start)
		el, ec := li.lspPos(end)
		return SymRange{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec}
	}

	type span struct {
		sym        *Symbol
		start, end uint32
	}
	var spans []span
	for _, t := range tagger.TagTree(tree) {
		kind, ok := kindFromTag(t.Kind)
		if !ok {
			continue
		}
		nameStart, nameEnd := t.NameRange.StartByte, t.NameRange.EndByte
		if nameEnd == 0 {
			nameStart, nameEnd = t.Range.StartByte, t.Range.StartByte
		}
		spans = append(spans, span{
			sym: &Symbol{
				Name:      t.Name,
				Kind:      kind,
				Range:     rangeAt(t.Range.StartByte, t.Range.EndByte),
				NameRange: rangeAt(nameStart, nameEnd),
			},
			start: t.Range.StartByte,
			end:   t.Range.EndByte,
		})
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})

	var roots []*Symbol
	var stack []span
	for _, s := range spans {
		for len(stack) > 0 && stack[len(stack)-1].end <= s.start {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, s.sym)
		} else {
			parent := stack[len(stack)-1].sym
			parent.Children = append(parent.Children, s.sym)
		}
		stack = append(stack, s)
	}
	return roots
}

// DocumentHighlights returns syntax-tree identifier occurrences matching the
// token at the cursor. It deliberately stays file-local: without type and
// scope information, highlighting same-spelled identifiers in other files
// would imply a certainty Tree-sitter does not provide.
func DocumentHighlights(filename string, src []byte, line, col int) []SymRange {
	entry := grammars.DetectLanguage(path.Base(filepath.ToSlash(filename)))
	if entry == nil || len(src) == 0 {
		return nil
	}

	ex := navExtractors.Get().(*navExtractor)
	defer navExtractors.Put(ex)
	name := ex.identifierAt(entry, src, line, col)
	if name == "" {
		return nil
	}
	tree, err := ex.parser(entry).Parse(src)
	if err != nil {
		return nil
	}
	defer tree.Release()

	li := newLineIndex(src)
	var result []SymRange
	var visit func(*ts.Node)
	visit = func(node *ts.Node) {
		if node == nil {
			return
		}
		if node.ChildCount() == 0 {
			if node.IsNamed() && node.Text(src) == name {
				sl, sc := li.lspPos(node.StartByte())
				el, ec := li.lspPos(node.EndByte())
				result = append(result, SymRange{StartLine: sl, StartCol: sc, EndLine: el, EndCol: ec})
			}
			return
		}
		for i := 0; i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(tree.RootNode())
	return result
}

// FoldingRanges returns conservative declaration-level folds. These are less
// exhaustive than a language server's ranges but stable across all grammars
// with tags support and never invent folds from indentation alone.
func FoldingRanges(filename string, src []byte) []SymRange {
	var result []SymRange
	var add func([]*Symbol)
	add = func(symbols []*Symbol) {
		for _, symbol := range symbols {
			if symbol.Range.EndLine > symbol.Range.StartLine {
				result = append(result, symbol.Range)
			}
			add(symbol.Children)
		}
	}
	add(FileSymbols(filename, src))
	return result
}

type SemanticToken struct {
	Range     SymRange
	Type      string
	Modifiers []string
}

// SemanticTokens classifies a buffer with the grammar's highlight query. It
// is syntactic rather than type-aware, but considerably richer and more
// accurate than lexical colorization when an LSP server is absent.
func SemanticTokens(filename string, src []byte) []SemanticToken {
	entry := grammars.DetectLanguage(path.Base(filepath.ToSlash(filename)))
	if entry == nil || entry.HighlightQuery == "" || len(src) == 0 {
		return nil
	}

	ex := navExtractors.Get().(*navExtractor)
	defer navExtractors.Put(ex)
	highlighter := ex.highlighters[entry.Name]
	if highlighter == nil {
		options := []ts.HighlighterOption{}
		if entry.TokenSourceFactory != nil {
			options = append(options, ts.WithTokenSourceFactory(func(source []byte) ts.TokenSource {
				return entry.TokenSourceFactory(source, entry.Language())
			}))
		}
		var err error
		highlighter, err = ts.NewHighlighter(entry.Language(), entry.HighlightQuery, options...)
		if err != nil {
			return nil
		}
		ex.highlighters[entry.Name] = highlighter
	}

	li := newLineIndex(src)
	result := make([]SemanticToken, 0)
	for _, highlight := range highlighter.Highlight(src) {
		tokenType, modifiers, ok := semanticCapture(highlight.Capture)
		if !ok {
			continue
		}
		start := highlight.StartByte
		for start < highlight.EndByte {
			line, col := li.lspPos(start)
			end := highlight.EndByte
			if line+1 < len(li.starts) && li.starts[line+1] <= end {
				end = li.starts[line+1]
				for end > start && (src[end-1] == '\n' || src[end-1] == '\r') {
					end--
				}
			}
			endLine, endCol := li.lspPos(end)
			if endLine == line && endCol > col {
				result = append(result, SemanticToken{
					Range:     SymRange{StartLine: line, StartCol: col, EndLine: endLine, EndCol: endCol},
					Type:      tokenType,
					Modifiers: modifiers,
				})
			}
			if line+1 >= len(li.starts) || li.starts[line+1] <= start {
				break
			}
			start = li.starts[line+1]
		}
	}
	return result
}

func semanticCapture(capture string) (string, []string, bool) {
	parts := strings.Split(capture, ".")
	var tokenType string
	for _, part := range parts {
		switch part {
		case "comment":
			tokenType = "comment"
		case "string", "character":
			tokenType = "string"
		case "number", "numeric", "float":
			tokenType = "number"
		case "keyword":
			tokenType = "keyword"
		case "operator":
			tokenType = "operator"
		case "method":
			tokenType = "method"
		case "function":
			if tokenType == "" {
				tokenType = "function"
			}
		case "class":
			tokenType = "class"
		case "interface":
			tokenType = "interface"
		case "struct":
			tokenType = "struct"
		case "enum":
			tokenType = "enum"
		case "type":
			if tokenType == "" {
				tokenType = "type"
			}
		case "parameter":
			tokenType = "parameter"
		case "variable":
			if tokenType == "" {
				tokenType = "variable"
			}
		case "property", "field":
			tokenType = "property"
		case "namespace", "module":
			tokenType = "namespace"
		case "macro":
			tokenType = "macro"
		case "label":
			tokenType = "label"
		case "regexp", "regex":
			tokenType = "regexp"
		case "decorator", "attribute":
			tokenType = "decorator"
		}
	}
	if tokenType == "" {
		return "", nil, false
	}
	modifiers := make([]string, 0, 2)
	for _, part := range parts {
		switch part {
		case "declaration", "definition", "readonly", "static", "deprecated", "abstract", "async", "modification", "documentation":
			modifiers = append(modifiers, part)
		case "builtin":
			modifiers = append(modifiers, "defaultLibrary")
		}
	}
	return tokenType, modifiers, true
}

// Definitions finds declaration sites for the identifier at the given
// position. src overrides the on-disk content when non-nil.
func (e *Engine) Definitions(ctx context.Context, file string, src []byte, line, col int) ([]Location, error) {
	name, lang, err := e.identifierFor(file, src, line, col)
	if err != nil || name == "" {
		return nil, err
	}

	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	cands := rankNodes(g, file, name, lang)
	if len(cands) > 50 {
		cands = cands[:50]
	}

	out := make([]Location, 0, len(cands))
	for _, n := range cands {
		out = append(out, nodeLocation(n))
	}
	return out, nil
}

func rankNodes(g *Graph, file, name, lang string) []*Node {
	fam := langFamily(lang)
	srcDir := path.Dir(file)
	imported := map[string]bool{}
	for _, im := range g.Imports {
		if im.FromFile == file && im.ToModule != "" {
			imported[im.ToModule] = true
		}
	}

	type scored struct {
		node  *Node
		score int
	}
	var cands []scored
	for _, n := range g.byName[name] {
		if langFamily(n.Lang) != fam {
			continue
		}
		score := 0
		switch {
		case n.File == file:
			score = 3
		case path.Dir(n.File) == srcDir:
			score = 2
		case imported[path.Dir(n.File)]:
			score = 1
		}
		cands = append(cands, scored{n, score})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].node.File != cands[j].node.File {
			return cands[i].node.File < cands[j].node.File
		}
		return cands[i].node.StartLine < cands[j].node.StartLine
	})

	out := make([]*Node, len(cands))
	for i, c := range cands {
		out[i] = c.node
	}
	return out
}

// References finds declaration and usage sites for the identifier at the
// given position. src overrides the on-disk content when non-nil.
func (e *Engine) References(ctx context.Context, file string, src []byte, line, col int) ([]Location, error) {
	name, lang, err := e.identifierFor(file, src, line, col)
	if err != nil || name == "" {
		return nil, err
	}

	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	fam := langFamily(lang)
	var out []Location
	seen := map[Location]bool{}
	add := func(l Location) {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	for _, n := range g.byName[name] {
		if langFamily(n.Lang) == fam {
			add(nodeLocation(n))
		}
	}
	for _, r := range g.refsByName[name] {
		if langFamily(r.Lang) == fam {
			add(Location{File: r.File, Line: r.Line, Col: r.Col})
		}
	}
	sortLocations(out)
	if len(out) > 500 {
		out = out[:500]
	}
	return out, nil
}

type HoverInfo struct {
	Node      *Node
	Code      string
	Truncated bool
	Others    int
}

// Hover returns the best-ranked definition for the identifier at the given
// position with a snippet of its source, or nil when nothing matches.
func (e *Engine) Hover(ctx context.Context, file string, src []byte, line, col int) (*HoverInfo, error) {
	name, lang, err := e.identifierFor(file, src, line, col)
	if err != nil || name == "" {
		return nil, err
	}

	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.RLock()
	cands := rankNodes(g, file, name, lang)
	e.mu.RUnlock()

	if len(cands) == 0 {
		return nil, nil
	}
	node := cands[0]
	code, truncated, err := e.snippetLines(node, 12)
	if err != nil {
		return nil, nil
	}
	return &HoverInfo{Node: node, Code: code, Truncated: truncated, Others: len(cands) - 1}, nil
}

// Implementations returns implementers and subtypes of the type named at the
// given position — only relations stated syntactically (extends/implements
// clauses, superclasses), so structurally-typed languages yield nothing.
func (e *Engine) Implementations(ctx context.Context, file string, src []byte, line, col int) ([]Location, error) {
	name, lang, err := e.identifierFor(file, src, line, col)
	if err != nil || name == "" {
		return nil, err
	}

	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	fam := langFamily(lang)
	var out []Location
	seen := map[Location]bool{}
	for _, n := range g.byName[name] {
		if langFamily(n.Lang) != fam {
			continue
		}
		for _, id := range append(append([]string(nil), g.implIn[n.ID]...), g.superIn[n.ID]...) {
			if t := g.byID[id]; t != nil && !seen[nodeLocation(t)] {
				seen[nodeLocation(t)] = true
				out = append(out, nodeLocation(t))
			}
		}
	}
	sortLocations(out)
	return out, nil
}

func (e *Engine) snippetLines(n *Node, maxLines int) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(n.File)))
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(string(data), "\n")
	if n.StartLine < 1 || n.StartLine > len(lines) {
		return "", false, errors.New("line range out of bounds")
	}
	end := min(n.EndLine, len(lines))
	truncated := false
	if end >= n.StartLine+maxLines {
		end = n.StartLine + maxLines - 1
		truncated = true
	}
	return strings.Join(lines[n.StartLine-1:end], "\n"), truncated, nil
}

func sortLocations(out []Location) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
}

func nodeLocation(n *Node) Location {
	if n.NameLine > 0 {
		return Location{File: n.File, Line: n.NameLine - 1, Col: max(n.NameCol-1, 0)}
	}
	return Location{File: n.File, Line: max(n.StartLine-1, 0)}
}

func (e *Engine) identifierFor(file string, src []byte, line, col int) (string, string, error) {
	entry := grammars.DetectLanguage(path.Base(file))
	if entry == nil {
		return "", "", nil
	}
	if src == nil {
		data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(file)))
		if err != nil {
			return "", "", err
		}
		src = data
	}

	ex := navExtractors.Get().(*navExtractor)
	defer navExtractors.Put(ex)
	return ex.identifierAt(entry, src, line, col), entry.Name, nil
}

func (ex *extractor) identifierAt(entry *grammars.LangEntry, src []byte, line, col int) string {
	off, ok := newLineIndex(src).byteAt(line, col)
	if !ok {
		return ""
	}
	tree, err := ex.parser(entry).Parse(src)
	if err != nil {
		return ""
	}
	defer tree.Release()
	root := tree.RootNode()
	if root == nil {
		return ""
	}

	candidates := []uint32{off}
	if off > 0 {
		candidates = append(candidates, off-1)
	}
	for _, o := range candidates {
		n := root.NamedNodeAtByte(o)
		if n == nil || n.ChildCount() > 0 {
			continue
		}
		if text := n.Text(src); isIdentifier(text) {
			return text
		}
	}
	return ""
}

func isIdentifier(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' || r == '$' || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

func langFamily(lang string) string {
	switch lang {
	case "javascript", "jsx", "typescript", "tsx":
		return "js"
	}
	return lang
}

func (l *lineIndex) byteAt(line, col int) (uint32, bool) {
	if line < 0 || line >= len(l.starts) || col < 0 {
		return 0, false
	}
	end := uint32(len(l.src))
	if line+1 < len(l.starts) {
		end = l.starts[line+1]
	}
	off := l.starts[line]
	units := 0
	for units < col && off < end {
		r, size := utf8.DecodeRune(l.src[off:end])
		if r == '\n' {
			break
		}
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
		off += uint32(size)
	}
	return off, true
}
