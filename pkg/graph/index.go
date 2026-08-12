package graph

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

const maxResolves = 2000

const maxFileBytes = 2 << 20

// maxAmbiguousFanout caps how many candidate targets a single name-resolved
// reference may fan out to. Names with more candidates than this (String,
// Error, Read, ...) are too common for a name guess to carry signal, so the
// reference is dropped rather than exploding the graph with noise edges.
// On the Go stdlib the median ambiguous name has ~9 candidates, so this keeps
// the genuinely narrow guesses and discards the long noise tail.
const maxAmbiguousFanout = 8

var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"target":       true,
	"out":          true,
	"bin":          true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	".tox":         true,
	"testdata":     true,
}

type fileMeta struct {
	MTime int64 `json:"mtime"`
	Size  int64 `json:"size"`
}

type indexStats struct {
	Files   int
	Nodes   int
	Edges   int
	Skipped []CoverageIssue
}

type indexRef struct {
	fromID string
	name   string
	file   string
	line   int
	col    int
	kind   EdgeKind
	lang   string
}

type fileResult struct {
	rel     string
	meta    fileMeta
	nodes   []*Node
	refs    []indexRef
	imports []rawImport
	issue   *CoverageIssue
}

func indexRepo(ctx context.Context, root string, resolver CallResolver) (*Graph, map[string]fileMeta, indexStats, error) {
	paths, err := collectFiles(ctx, root)
	if err != nil {
		return nil, nil, indexStats{}, err
	}

	results, err := parseFiles(ctx, root, paths)
	if err != nil {
		return nil, nil, indexStats{}, err
	}

	// Keep metadata for every discovered source file, including files whose
	// structure could not be extracted. It drives cache freshness and lets
	// content search preserve raw hits from graph coverage gaps.
	files := make(map[string]fileMeta, len(results))
	var nodes []*Node
	var refs []indexRef
	var skipped []CoverageIssue
	indexedFiles := 0
	for _, r := range results {
		if r.meta.Size >= 0 {
			files[r.rel] = r.meta
		}
		if r.issue != nil {
			skipped = append(skipped, *r.issue)
			continue
		}
		indexedFiles++
		nodes = append(nodes, r.nodes...)
		refs = append(refs, r.refs...)
	}

	g := &Graph{Nodes: nodes}
	g.build()

	seen := map[string]bool{}
	addEdge := func(from, to string, kind EdgeKind, via Provenance) {
		if from == to {
			return
		}
		key := from + "\x00" + to + "\x00" + string(kind)
		if seen[key] {
			return
		}
		seen[key] = true
		g.Edges = append(g.Edges, &Edge{From: from, To: to, Kind: kind, Via: via})
	}

	resolves := 0
	processed := make(map[string]bool, len(refs))
	for _, r := range refs {
		if r.fromID == "" {
			continue
		}
		pk := r.fromID + "\x00" + r.name + "\x00" + string(r.kind)
		if processed[pk] {
			continue
		}
		processed[pk] = true

		var cands []*Node
		for _, c := range g.byName[r.name] {
			if langFamily(c.Lang) == langFamily(r.lang) {
				cands = append(cands, c)
			}
		}
		switch len(cands) {
		case 0:
			continue
		case 1:
			addEdge(r.fromID, cands[0].ID, r.kind, ViaName)
		default:
			if resolver != nil && resolves < maxResolves {
				resolves++
				if df, dl, ok := resolver.ResolveCall(ctx, r.file, r.line, r.col); ok {
					if tgt := g.nodeAt(df, dl); tgt != nil {
						addEdge(r.fromID, tgt.ID, r.kind, ViaLSP)
						continue
					}
				}
			}
			if len(cands) > maxAmbiguousFanout {
				continue
			}
			for _, cand := range cands {
				addEdge(r.fromID, cand.ID, r.kind, ViaAmbiguous)
			}
		}
	}

	g.Refs = make([]*Ref, 0, len(refs))
	for _, r := range refs {
		g.Refs = append(g.Refs, &Ref{Name: r.name, File: r.file, Line: r.line, Col: r.col, Kind: r.kind, Lang: r.lang})
	}

	localDirs := make(map[string]bool, len(files))
	for f := range files {
		localDirs[path.Dir(f)] = true
	}
	for _, r := range results {
		for _, im := range r.imports {
			g.Imports = append(g.Imports, &Import{
				FromFile: r.rel,
				Path:     im.norm,
				ToModule: resolveImport(r.rel, im.norm, im.rel, localDirs),
			})
		}
	}

	g.build()

	sort.Slice(skipped, func(i, j int) bool { return skipped[i].File < skipped[j].File })
	stats := indexStats{Files: indexedFiles, Nodes: len(g.Nodes), Edges: len(g.Edges), Skipped: skipped}
	return g, files, stats, nil
}

func collectFiles(ctx context.Context, root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if grammars.DetectLanguage(name) == nil {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	return paths, err
}

func parseFiles(ctx context.Context, root string, paths []string) ([]*fileResult, error) {
	workers := min(runtime.GOMAXPROCS(0), len(paths))
	if workers < 1 {
		return nil, nil
	}

	results := make([]*fileResult, len(paths))
	var next atomic.Int64
	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			ex := newExtractor()
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= len(paths) {
					return
				}
				results[i] = ex.processFile(root, paths[i])
			}
		})
	}
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	out := results[:0]
	for _, r := range results {
		if r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// extractor holds the per-worker tree-sitter state. Taggers, parsers and
// compiled queries are not safe for concurrent use, so each worker owns one.
type extractor struct {
	taggers map[string]*ts.Tagger
	parsers map[string]*ts.Parser
	ax      *auxExtractor
}

func newExtractor() *extractor {
	return &extractor{
		taggers: map[string]*ts.Tagger{},
		parsers: map[string]*ts.Parser{},
		ax:      newAuxExtractor(),
	}
}

func (ex *extractor) parser(entry *grammars.LangEntry) *ts.Parser {
	p := ex.parsers[entry.Name]
	if p == nil {
		p = ts.NewParser(entry.Language())
		ex.parsers[entry.Name] = p
	}
	return p
}

func (ex *extractor) tagger(entry *grammars.LangEntry) *ts.Tagger {
	if t, ok := ex.taggers[entry.Name]; ok {
		return t
	}
	var t *ts.Tagger
	if q := grammars.ResolveTagsQuery(*entry); q != "" {
		if aug := tagsAugment[entry.Name]; aug != "" {
			q += "\n" + aug
		}
		t, _ = ts.NewTagger(entry.Language(), q)
	}
	ex.taggers[entry.Name] = t
	return t
}

func (ex *extractor) processFile(root, absPath string) *fileResult {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		rel = absPath
	}
	rel = filepath.ToSlash(rel)
	res := &fileResult{rel: rel, meta: fileMeta{Size: -1}}
	fail := func(reason string) *fileResult {
		res.issue = &CoverageIssue{File: rel, Reason: reason}
		return res
	}

	entry := grammars.DetectLanguage(filepath.Base(absPath))
	if entry == nil {
		return fail("unsupported_language")
	}
	tagger := ex.tagger(entry)
	if tagger == nil {
		return fail("no_tags_query")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fail("stat_failed")
	}
	res.meta = fileMeta{MTime: info.ModTime().UnixNano(), Size: info.Size()}
	if info.Size() == 0 {
		return fail("empty")
	}
	if info.Size() > maxFileBytes {
		return fail("oversized")
	}
	src, err := os.ReadFile(absPath)
	if err != nil {
		return fail("read_failed")
	}
	if looksMinified(src) {
		return fail("minified")
	}

	tree, err := ex.parser(entry).Parse(src)
	if err != nil {
		return fail("parse_failed")
	}
	defer tree.Release()
	rn := tree.RootNode()
	if rn == nil {
		return fail("empty_syntax_tree")
	}

	li := newLineIndex(src)
	tags := tagger.TagTree(tree)

	type defSpan struct {
		id    string
		start uint32
		end   uint32
		kind  Kind
	}
	var defs []defSpan

	for _, t := range tags {
		kind, ok := kindFromTag(t.Kind)
		if !ok {
			continue
		}
		id := fmt.Sprintf("%s#%s@%d", rel, t.Name, t.Range.StartByte)
		namePos := t.NameRange.StartByte
		if t.NameRange.EndByte == 0 {
			namePos = t.Range.StartByte
		}
		nameLine, nameCol := li.lspPos(namePos)
		res.nodes = append(res.nodes, &Node{
			ID:        id,
			Kind:      kind,
			Name:      t.Name,
			File:      rel,
			StartLine: li.line(t.Range.StartByte),
			EndLine:   li.line(t.Range.EndByte),
			NameLine:  nameLine + 1,
			NameCol:   nameCol + 1,
			Lang:      entry.Name,
		})
		defs = append(defs, defSpan{id: id, start: t.Range.StartByte, end: t.Range.EndByte, kind: kind})
	}

	sort.Slice(defs, func(i, j int) bool {
		return (defs[i].end - defs[i].start) < (defs[j].end - defs[j].start)
	})

	enclosing := func(b uint32, typesOnly bool) string {
		for _, ds := range defs {
			if b < ds.start || b >= ds.end {
				continue
			}
			if typesOnly && !isTypeKind(ds.kind) {
				continue
			}
			return ds.id
		}
		return ""
	}

	for _, t := range tags {
		if !isCallTag(t.Kind) {
			continue
		}
		fromID := enclosing(t.Range.StartByte, false)
		pos := t.NameRange.StartByte
		if t.NameRange.EndByte == 0 {
			pos = t.Range.StartByte
		}
		line, col := li.lspPos(pos)
		res.refs = append(res.refs, indexRef{fromID: fromID, name: t.Name, file: rel, line: line, col: col, kind: EdgeCalls, lang: entry.Name})
	}

	imps, hiers := ex.ax.extractFromTree(entry.Name, entry.Language(), rn, src)
	res.imports = imps
	for _, hr := range hiers {
		fromID := enclosing(hr.startByte, true)
		line, col := li.lspPos(hr.startByte)
		res.refs = append(res.refs, indexRef{fromID: fromID, name: hr.name, file: rel, line: line, col: col, kind: hr.kind, lang: entry.Name})
	}

	return res
}

func looksMinified(src []byte) bool {
	const maxLine = 2000
	last := -1
	for i := range src {
		if src[i] == '\n' {
			if i-last-1 > maxLine {
				return true
			}
			last = i
		}
	}
	return len(src)-last-1 > maxLine
}

type lineIndex struct {
	src    []byte
	starts []uint32
}

func newLineIndex(src []byte) *lineIndex {
	starts := []uint32{0}
	for i := range src {
		if src[i] == '\n' {
			starts = append(starts, uint32(i+1))
		}
	}
	return &lineIndex{src: src, starts: starts}
}

func (l *lineIndex) line(byteOff uint32) int {
	lo, hi := 0, len(l.starts)
	for lo < hi {
		mid := (lo + hi) / 2
		if l.starts[mid] <= byteOff {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (l *lineIndex) lspPos(byteOff uint32) (line, column int) {
	ln := l.line(byteOff)
	return ln - 1, utf16Len(l.src[l.starts[ln-1]:byteOff])
}

func utf16Len(b []byte) int {
	n := 0
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		b = b[size:]
	}
	return n
}
