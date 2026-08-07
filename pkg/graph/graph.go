package graph

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type Graph struct {
	Nodes   []*Node   `json:"nodes"`
	Edges   []*Edge   `json:"edges"`
	Imports []*Import `json:"imports,omitempty"`

	byID    map[string]*Node
	byName  map[string][]*Node
	byFile  map[string][]*Node
	tokens  map[string][]string
	out     map[string][]string
	in      map[string][]string
	edgeVia map[string]Provenance

	superOut map[string][]string
	superIn  map[string][]string
	implOut  map[string][]string
	implIn   map[string][]string

	moduleSet map[string]bool
	modOut    map[string]map[string]bool
	modIn     map[string]map[string]bool
	modExt    map[string]map[string]bool
}

func (g *Graph) build() {
	g.byID = make(map[string]*Node, len(g.Nodes))
	g.byName = make(map[string][]*Node)
	g.byFile = make(map[string][]*Node)
	g.out = make(map[string][]string)
	g.in = make(map[string][]string)
	g.edgeVia = make(map[string]Provenance)
	g.superOut = make(map[string][]string)
	g.superIn = make(map[string][]string)
	g.implOut = make(map[string][]string)
	g.implIn = make(map[string][]string)

	g.tokens = make(map[string][]string, len(g.Nodes))

	for _, n := range g.Nodes {
		g.byID[n.ID] = n
		g.byName[n.Name] = append(g.byName[n.Name], n)
		g.byFile[n.File] = append(g.byFile[n.File], n)
		g.tokens[n.ID] = tokenize(n.Name)
	}

	for _, e := range g.Edges {
		switch e.Kind {
		case EdgeCalls:
			g.out[e.From] = append(g.out[e.From], e.To)
			g.in[e.To] = append(g.in[e.To], e.From)
			g.edgeVia[e.From+"\x00"+e.To] = e.Via
		case EdgeInherits:
			g.superOut[e.From] = append(g.superOut[e.From], e.To)
			g.superIn[e.To] = append(g.superIn[e.To], e.From)
		case EdgeImplements:
			g.implOut[e.From] = append(g.implOut[e.From], e.To)
			g.implIn[e.To] = append(g.implIn[e.To], e.From)
		}
	}

	g.moduleSet = map[string]bool{}
	g.modOut = map[string]map[string]bool{}
	g.modIn = map[string]map[string]bool{}
	g.modExt = map[string]map[string]bool{}

	for _, n := range g.Nodes {
		g.moduleSet[path.Dir(n.File)] = true
	}
	for _, im := range g.Imports {
		from := path.Dir(im.FromFile)
		g.moduleSet[from] = true
		if im.ToModule == "" {
			addToSet(g.modExt, from, im.Path)
			continue
		}
		g.moduleSet[im.ToModule] = true
		if im.ToModule == from {
			continue
		}
		addToSet(g.modOut, from, im.ToModule)
		addToSet(g.modIn, im.ToModule, from)
	}
}

func addToSet(m map[string]map[string]bool, k, v string) {
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	m[k][v] = true
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type SearchOpts struct {
	Query  string
	Kind   Kind
	File   string
	Limit  int
	Offset int
}

type SearchResult struct {
	Nodes   []*Node `json:"nodes"`
	Total   int     `json:"total"`
	Offset  int     `json:"offset"`
	HasMore bool    `json:"has_more"`
}

func (g *Graph) search(opts SearchOpts) []*Node {
	return g.searchPage(opts).Nodes
}

func (g *Graph) searchPage(opts SearchOpts) SearchResult {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	var rx *regexp.Regexp
	if opts.Query != "" {
		if r, err := regexp.Compile("(?i)" + opts.Query); err == nil {
			rx = r
		}
	}
	qLower := strings.ToLower(strings.TrimSpace(opts.Query))
	qTokens := tokenize(opts.Query)

	type scored struct {
		node  *Node
		score int
	}
	var out []scored
	for _, n := range g.Nodes {
		if opts.Kind != "" && n.Kind != opts.Kind {
			continue
		}
		if opts.File != "" && !strings.Contains(n.File, opts.File) {
			continue
		}
		score := g.matchScore(n, qLower, qTokens, rx)
		if score == 0 {
			continue
		}
		out = append(out, scored{n, score + kindBoost(n.Kind) + degreeBoost(len(g.in[n.ID]))})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].node.Name != out[j].node.Name {
			return out[i].node.Name < out[j].node.Name
		}
		return out[i].node.File < out[j].node.File
	})

	total := len(out)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := out[offset:end]
	nodes := make([]*Node, len(page))
	for i, s := range page {
		nodes[i] = s.node
	}
	return SearchResult{Nodes: nodes, Total: total, Offset: offset, HasMore: end < total}
}

func (g *Graph) matchScore(n *Node, qLower string, qTokens []string, rx *regexp.Regexp) int {
	if qLower == "" {
		return 1
	}
	nameLower := strings.ToLower(n.Name)
	switch {
	case nameLower == qLower:
		return 100
	case strings.HasPrefix(nameLower, qLower):
		return 70
	case containsAllTokens(g.tokens[n.ID], qTokens):
		return 55
	case strings.Contains(nameLower, qLower):
		return 45
	case rx != nil && rx.MatchString(n.Name):
		return 40
	}
	return 0
}

func kindBoost(k Kind) int {
	switch k {
	case KindFunction, KindMethod, KindConstructor:
		return 12
	case KindClass, KindInterface:
		return 10
	case KindType, KindModule:
		return 6
	}
	return 0
}

func degreeBoost(callers int) int {
	switch {
	case callers >= 25:
		return 8
	case callers >= 10:
		return 6
	case callers >= 3:
		return 4
	case callers >= 1:
		return 2
	}
	return 0
}

func tokenize(s string) []string {
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(s)
	for i, r := range runes {
		switch {
		case unicode.IsLetter(r):
			if unicode.IsUpper(r) && i > 0 {
				prevLower := unicode.IsLower(runes[i-1])
				acronymEnd := unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if prevLower || acronymEnd {
					flush()
				}
			}
			cur = append(cur, r)
		case unicode.IsDigit(r):
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func containsAllTokens(have, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.HasPrefix(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// resolve finds definitions for a symbol reference. Besides a bare name it
// accepts qualified forms: `path:Name` (path substring before a colon) and,
// when the bare name has no match, `pkg.Name` or `dir/Name` where the prefix
// filters by file path.
func (g *Graph) resolve(symbol, file string) []*Node {
	name := symbol
	var prefix string

	if i := strings.LastIndex(symbol, ":"); i > 0 {
		prefix, name = symbol[:i], symbol[i+1:]
	} else if len(g.byName[symbol]) == 0 {
		if i := strings.LastIndexAny(symbol, "./"); i > 0 {
			prefix, name = symbol[:i], symbol[i+1:]
		}
	}

	var out []*Node
	for _, n := range g.byName[name] {
		if file != "" && !strings.Contains(n.File, file) {
			continue
		}
		if prefix != "" && !strings.Contains(n.File, prefix) && !strings.Contains(n.File, strings.ReplaceAll(prefix, ".", "/")) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (g *Graph) nodesFor(ids []string) []*Node {
	seen := make(map[string]bool, len(ids))
	var out []*Node
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if n := g.byID[id]; n != nil {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].File < out[j].File
	})
	return out
}

type HierarchyResult struct {
	Type         *Node   `json:"type"`
	Extends      []*Node `json:"extends"`
	Subtypes     []*Node `json:"subtypes"`
	Implements   []*Node `json:"implements"`
	Implementers []*Node `json:"implementers"`
	Others       []*Node `json:"others,omitempty"`
}

func (g *Graph) hierarchy(id string) HierarchyResult {
	return HierarchyResult{
		Type:         g.byID[id],
		Extends:      g.nodesFor(g.superOut[id]),
		Subtypes:     g.nodesFor(g.superIn[id]),
		Implements:   g.nodesFor(g.implOut[id]),
		Implementers: g.nodesFor(g.implIn[id]),
	}
}

func (g *Graph) nodeAt(file string, line int) *Node {
	var best *Node
	for _, n := range g.byFile[file] {
		if line < n.StartLine || line > n.EndLine {
			continue
		}
		if best == nil || (n.EndLine-n.StartLine) < (best.EndLine-best.StartLine) {
			best = n
		}
	}
	return best
}

func (g *Graph) deadCode(limit int) []*Node {
	if limit <= 0 {
		limit = 100
	}
	var out []*Node
	for _, n := range g.Nodes {
		switch n.Kind {
		case KindFunction, KindMethod, KindConstructor:
		default:
			continue
		}
		if n.Name == "main" || n.Name == "Main" || n.Name == "init" {
			continue
		}
		if len(g.in[n.ID]) == 0 {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].StartLine < out[j].StartLine
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

type DepsResult struct {
	Module     string   `json:"module"`
	DependsOn  []string `json:"depends_on"`
	DependedBy []string `json:"depended_by"`
	External   []string `json:"external"`
	Transitive []string `json:"transitive,omitempty"`
}

func (g *Graph) resolveModule(target string) string {
	t := strings.Trim(target, "/")
	if t == "" {
		return "."
	}
	if g.moduleSet[t] {
		return t
	}
	if d := path.Dir(t); g.moduleSet[d] {
		return d
	}
	best := ""
	for m := range g.moduleSet {
		if strings.Contains(m, t) && (best == "" || len(m) < len(best)) {
			best = m
		}
	}
	if best != "" {
		return best
	}
	return t
}

func (g *Graph) deps(module string, depth int) DepsResult {
	res := DepsResult{
		Module:     module,
		DependsOn:  sortedKeys(g.modOut[module]),
		DependedBy: sortedKeys(g.modIn[module]),
		External:   sortedKeys(g.modExt[module]),
	}
	if depth > 1 {
		res.Transitive = g.transitiveDeps(module, depth)
	}
	return res
}

func (g *Graph) transitiveDeps(module string, depth int) []string {
	seen := map[string]bool{module: true}
	for d := range g.modOut[module] {
		seen[d] = true
	}

	type item struct {
		mod string
		d   int
	}
	var queue []item
	for d := range g.modOut[module] {
		queue = append(queue, item{d, 1})
	}

	indirect := map[string]bool{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.d >= depth {
			continue
		}
		for next := range g.modOut[cur.mod] {
			if seen[next] {
				continue
			}
			seen[next] = true
			indirect[next] = true
			queue = append(queue, item{next, cur.d + 1})
		}
	}
	return sortedKeys(indirect)
}

type Path struct {
	Nodes []*Node
	Via   []Provenance
}

const maxTracePaths = 200

func (g *Graph) trace(fromID, toName string, callers bool, maxDepth int) []Path {
	if maxDepth <= 0 {
		maxDepth = 8
	}

	adj := g.out
	if callers {
		adj = g.in
	}

	type frame struct {
		id   string
		path []string
	}

	var paths []Path
	visited := map[string]bool{fromID: true}
	queue := []frame{{id: fromID, path: []string{fromID}}}

	for len(queue) > 0 && len(paths) < maxTracePaths {
		cur := queue[0]
		queue = queue[1:]

		if len(cur.path) > maxDepth {
			continue
		}

		next := adj[cur.id]

		if toName == "" {
			if len(cur.path) > 1 {
				paths = append(paths, g.pathFromIDs(cur.path, callers))
			}
		}

		for _, nid := range next {
			n := g.byID[nid]
			if n == nil {
				continue
			}
			np := append(append([]string{}, cur.path...), nid)
			if toName != "" && n.Name == toName {
				paths = append(paths, g.pathFromIDs(np, callers))
				continue
			}
			if visited[nid] {
				continue
			}
			visited[nid] = true
			queue = append(queue, frame{id: nid, path: np})
		}
	}

	return paths
}

func (g *Graph) pathFromIDs(ids []string, callers bool) Path {
	p := Path{}
	for _, id := range ids {
		if n := g.byID[id]; n != nil {
			p.Nodes = append(p.Nodes, n)
		}
	}
	for i := 0; i+1 < len(ids); i++ {
		from, to := ids[i], ids[i+1]
		if callers {
			from, to = to, from
		}
		p.Via = append(p.Via, g.edgeVia[from+"\x00"+to])
	}
	return p
}

type LangStat struct {
	Lang  string `json:"lang"`
	Files int    `json:"files"`
	Nodes int    `json:"nodes"`
}

type Hotspot struct {
	Node    *Node `json:"node"`
	Callers int   `json:"callers"`
	Callees int   `json:"callees"`
}

type ModuleStat struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Nodes int    `json:"nodes"`
}

type ModuleDep struct {
	Module     string `json:"module"`
	DependsOn  int    `json:"depends_on"`
	DependedBy int    `json:"depended_by"`
}

type Arch struct {
	Languages   []LangStat   `json:"languages"`
	TotalFiles  int          `json:"total_files"`
	TotalNodes  int          `json:"total_nodes"`
	TotalEdges  int          `json:"total_edges"`
	Layers      []ModuleStat `json:"layers"`
	Modules     []ModuleStat `json:"modules"`
	ModuleDeps  []ModuleDep  `json:"module_deps"`
	EntryPoints []*Node      `json:"entry_points"`
	Hotspots    []Hotspot    `json:"hotspots"`
}

func (g *Graph) architecture() Arch {
	langFiles := map[string]map[string]bool{}
	langNodes := map[string]int{}

	for _, n := range g.Nodes {
		if langFiles[n.Lang] == nil {
			langFiles[n.Lang] = map[string]bool{}
		}
		langFiles[n.Lang][n.File] = true
		langNodes[n.Lang]++
	}

	var langs []LangStat
	allFiles := map[string]bool{}
	for lang, files := range langFiles {
		langs = append(langs, LangStat{Lang: lang, Files: len(files), Nodes: langNodes[lang]})
		for f := range files {
			allFiles[f] = true
		}
	}
	sort.Slice(langs, func(i, j int) bool {
		if langs[i].Nodes != langs[j].Nodes {
			return langs[i].Nodes > langs[j].Nodes
		}
		return langs[i].Lang < langs[j].Lang
	})

	layers := groupByPath(g.Nodes, func(file string) string {
		seg, _, _ := strings.Cut(file, "/")
		return seg
	})
	modules := groupByPath(g.Nodes, path.Dir)
	if len(modules) > 15 {
		modules = modules[:15]
	}

	var modDeps []ModuleDep
	for m := range g.moduleSet {
		out, in := len(g.modOut[m]), len(g.modIn[m])
		if out+in == 0 {
			continue
		}
		modDeps = append(modDeps, ModuleDep{Module: m, DependsOn: out, DependedBy: in})
	}
	sort.Slice(modDeps, func(i, j int) bool {
		if modDeps[i].DependedBy != modDeps[j].DependedBy {
			return modDeps[i].DependedBy > modDeps[j].DependedBy
		}
		if modDeps[i].DependsOn != modDeps[j].DependsOn {
			return modDeps[i].DependsOn > modDeps[j].DependsOn
		}
		return modDeps[i].Module < modDeps[j].Module
	})
	if len(modDeps) > 15 {
		modDeps = modDeps[:15]
	}

	var entries []*Node
	for _, n := range g.Nodes {
		if (n.Kind == KindFunction || n.Kind == KindMethod) && (n.Name == "main" || n.Name == "Main") {
			entries = append(entries, n)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].File < entries[j].File })

	hot := make([]Hotspot, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		ci, co := len(g.in[n.ID]), len(g.out[n.ID])
		if ci+co == 0 {
			continue
		}
		hot = append(hot, Hotspot{Node: n, Callers: ci, Callees: co})
	}
	sort.Slice(hot, func(i, j int) bool {
		di := hot[i].Callers + hot[i].Callees
		dj := hot[j].Callers + hot[j].Callees
		if di != dj {
			return di > dj
		}
		return hot[i].Node.Name < hot[j].Node.Name
	})
	if len(hot) > 15 {
		hot = hot[:15]
	}

	return Arch{
		Languages:   langs,
		TotalFiles:  len(allFiles),
		TotalNodes:  len(g.Nodes),
		TotalEdges:  len(g.Edges),
		Layers:      layers,
		Modules:     modules,
		ModuleDeps:  modDeps,
		EntryPoints: entries,
		Hotspots:    hot,
	}
}

func groupByPath(nodes []*Node, key func(file string) string) []ModuleStat {
	files := map[string]map[string]bool{}
	count := map[string]int{}
	for _, n := range nodes {
		k := key(n.File)
		if files[k] == nil {
			files[k] = map[string]bool{}
		}
		files[k][n.File] = true
		count[k]++
	}

	out := make([]ModuleStat, 0, len(count))
	for k := range count {
		out = append(out, ModuleStat{Path: k, Files: len(files[k]), Nodes: count[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Nodes != out[j].Nodes {
			return out[i].Nodes > out[j].Nodes
		}
		return out[i].Path < out[j].Path
	})
	return out
}
