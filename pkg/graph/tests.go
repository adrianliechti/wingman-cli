package graph

import "strings"

// isTestFile reports whether a path looks like a test file, using common
// cross-language conventions (Go `_test.go`, Python `test_*`/`tests/`,
// JS/TS `*.test.*`/`*.spec.*`, and `test`/`spec` directories).
func isTestFile(path string) bool {
	p := strings.ToLower(path)
	for seg := range strings.SplitSeq(p, "/") {
		switch seg {
		case "test", "tests", "__tests__", "spec", "specs":
			return true
		}
	}

	base := p
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		base = p[i+1:]
	}
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") {
		return true
	}

	name := base
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		name = base[:i]
	}
	return strings.HasSuffix(name, "_test")
}

type TestsResult struct {
	Symbol   *Node   `json:"symbol"`
	TestedBy []*Node `json:"tested_by"`
	Covers   []*Node `json:"covers"`
	Others   []*Node `json:"others,omitempty"`
}

// testsFor derives the test relationship from CALLS edges: for a production
// symbol it returns the test functions that call it; for a test symbol it
// returns the production symbols it exercises.
func (g *Graph) testsFor(node *Node) TestsResult {
	res := TestsResult{Symbol: node}

	if isTestFile(node.File) {
		var ids []string
		for _, id := range g.out[node.ID] {
			if n := g.byID[id]; n != nil && !isTestFile(n.File) {
				ids = append(ids, id)
			}
		}
		res.Covers = g.nodesFor(ids)
		return res
	}

	var ids []string
	for _, id := range g.in[node.ID] {
		if n := g.byID[id]; n != nil && isTestFile(n.File) {
			ids = append(ids, id)
		}
	}
	res.TestedBy = g.nodesFor(ids)
	return res
}
