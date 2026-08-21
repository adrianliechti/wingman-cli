package graph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/agent/tool"
	"github.com/adrianliechti/wingman-agent/pkg/graph"
)

func NewTools(engine *graph.Engine) []tool.Tool {
	if engine == nil {
		return nil
	}
	return []tool.Tool{graphTool(engine)}
}

func graphTool(engine *graph.Engine) tool.Tool {
	return tool.Tool{
		Name: "code_graph",
		Description: strings.Join([]string{
			"Code knowledge graph (tree-sitter, many languages): definitions and their call/type/import links. Use instead of grep/read loops to find symbols and follow relationships; auto-builds on first use. With a language server, prefer `lsp` for precise definitions/references/types.",
			"Operations (each uses the field in backticks):",
			"- search `query`: definitions by name, word tokens (\"update client\" finds UpdateCloudClient), or regex — ranked by relevance; optional `kind`, `file`.",
			"- search_content `pattern`: literal source search enriched with containing definitions, match lines, and graph rank; deduplicates repeated lines in one symbol and preserves raw hits outside definitions. Set `regex` for RE2 syntax; optional `file`, `glob`, `ignore_case`.",
			"- trace `symbol`: call paths; `direction` callees (default) or callers; optional `target`, `file`.",
			"- find_similar `symbol`: functions resembling it (shared callees + name).",
			"- hierarchy `symbol`: super/sub types and implementers.",
			"- tests `symbol`: tests covering it, or what a test covers.",
			"- snippet `symbol`: its source code.",
			"- deps `file`: module imports/importers; `depth`>1 for transitive.",
			"- co_changes `file`: files historically committed together with it.",
			"- changes: uncommitted edits (or everything since a git ref via `since`) mapped to definitions, plus their unchanged callers (impact).",
			"- architecture: overview of languages, modules, entry points, hotspots.",
			"- dead_code: callables with no known caller (candidates; misses reflection/exported/cross-language).",
			"- index / status: rebuild the graph / report its state.",
		}, "\n"),
		Effect: tool.StaticEffect(tool.EffectReadOnly),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"enum":        []string{"index", "status", "search", "search_content", "trace", "architecture", "dead_code", "changes", "deps", "hierarchy", "snippet", "tests", "co_changes", "find_similar"},
					"description": "Which operation to run.",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "search: symbol name, space-separated word tokens, or regex (case-insensitive); results ranked by match quality, kind, and callers.",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "search_content: literal text to find in source files, or an RE2 expression when regex=true.",
				},
				"regex": map[string]any{
					"type":        "boolean",
					"description": "search_content: interpret pattern as an RE2 regular expression; defaults to false (literal).",
					"default":     false,
				},
				"ignore_case": map[string]any{
					"type":        "boolean",
					"description": "search_content: case-insensitive matching.",
					"default":     false,
				},
				"glob": map[string]any{
					"type":        "string",
					"description": "search_content: optional source-file glob, e.g. `*.go` or `pkg/**/*.go`.",
				},
				"symbol": map[string]any{
					"type":        "string",
					"description": "trace/find_similar/hierarchy/tests/snippet: one exact symbol name; qualify as `pkg.Name` or `path:Name` (path substring) when several definitions share the name.",
				},
				"target": map[string]any{
					"type":        "string",
					"description": "trace: optional destination symbol to reach.",
				},
				"direction": map[string]any{
					"type":        "string",
					"enum":        []string{"callees", "callers"},
					"description": "trace: callees (default) or callers.",
				},
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{"function", "method", "class", "interface", "type", "constructor", "module", "constant", "variable"},
					"description": "search: restrict to a definition kind.",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "deps: target module/dir/file. co_changes: exact repo-relative path. search/search_content/trace/snippet/hierarchy/tests/find_similar: optional path-substring filter.",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "changes: git ref to diff from (e.g. main, HEAD~5); default compares only uncommitted edits against HEAD.",
				},
				"depth": map[string]any{
					"type":        "integer",
					"description": "trace path depth (default 8); deps transitive depth (default 1).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "max results — search (default 50), search_content (10), dead_code (100), co_changes/find_similar (15).",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "search/search_content: skip the first N ranked results for pagination.",
					"default":     0,
				},
			},
			"required":             []string{"operation"},
			"additionalProperties": false,
		},
		Execute: func(ctx context.Context, args map[string]any) (tool.Result, error) {
			operation, _ := args["operation"].(string)

			switch operation {
			case "index":
				status, err := engine.Index(ctx)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(fmt.Sprintf("Indexed %d source files: %d definitions, %d call edges%s.%s",
					status.Files, status.Nodes, status.Edges, edgeBreakdown(engine.EdgeStats()), formatCoverage(status.Skipped))), nil

			case "status":
				st := engine.StatusOrLoad()
				if !st.Indexed {
					return tool.Text("Not indexed yet. Run operation \"index\" (or any query auto-builds it)."), nil
				}
				freshness := "fresh"
				if engine.IsStale(ctx) {
					freshness = "stale — the next graph query will refresh it"
				}
				return tool.Text(fmt.Sprintf("Indexed at %s (%s): %d source files, %d definitions, %d call edges%s.%s",
					st.IndexedAt.Format(time.RFC3339), freshness, st.Files, st.Nodes, st.Edges, edgeBreakdown(engine.EdgeStats()), formatCoverage(st.Skipped))), nil

			case "search":
				query, _ := args["query"].(string)
				limit, _, err := tool.NonNegIntArg(args, "limit")
				if err != nil {
					return tool.Result{}, err
				}
				offset, _, err := tool.NonNegIntArg(args, "offset")
				if err != nil {
					return tool.Result{}, err
				}
				opts := graph.SearchOpts{
					Query:  query,
					Kind:   graph.Kind(strings.TrimSpace(stringArg(args, "kind"))),
					File:   stringArg(args, "file"),
					Limit:  limit,
					Offset: offset,
				}
				res, err := engine.SearchPage(ctx, opts)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatSearchResult(res)), nil

			case "search_content":
				limit, _, err := tool.NonNegIntArg(args, "limit")
				if err != nil {
					return tool.Result{}, err
				}
				offset, _, err := tool.NonNegIntArg(args, "offset")
				if err != nil {
					return tool.Result{}, err
				}
				res, err := engine.SearchContent(ctx, graph.ContentSearchOpts{
					Pattern:    stringArg(args, "pattern"),
					Regex:      boolArg(args, "regex"),
					IgnoreCase: boolArg(args, "ignore_case"),
					File:       stringArg(args, "file"),
					Glob:       stringArg(args, "glob"),
					Limit:      limit,
					Offset:     offset,
				})
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatContentSearch(res)), nil

			case "trace":
				symbol := strings.TrimSpace(stringArg(args, "symbol"))
				if symbol == "" {
					return tool.Result{}, fmt.Errorf("symbol is required for trace")
				}
				callers := stringArg(args, "direction") == "callers"
				depth, _ := tool.IntArg(args, "depth")
				res, err := engine.Trace(ctx, symbol, strings.TrimSpace(stringArg(args, "target")), stringArg(args, "file"), callers, depth)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatTrace(res, callers)), nil

			case "architecture":
				arch, err := engine.Architecture(ctx)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatArch(arch)), nil

			case "dead_code":
				limit, _ := tool.IntArg(args, "limit")
				nodes, err := engine.DeadCode(ctx, limit)
				if err != nil {
					return tool.Result{}, err
				}
				if len(nodes) == 0 {
					return tool.Text("No dead code found (every callable has a detected caller)."), nil
				}
				return tool.Text(formatNodes(nodes)), nil

			case "changes":
				changes, err := engine.DetectChanges(ctx, strings.TrimSpace(stringArg(args, "since")))
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatChanges(changes)), nil

			case "deps":
				target := strings.TrimSpace(stringArg(args, "file"))
				if target == "" {
					target = strings.TrimSpace(stringArg(args, "symbol"))
				}
				if target == "" {
					return tool.Result{}, fmt.Errorf("file (a module/dir or file path) is required for deps")
				}
				depth, _ := tool.IntArg(args, "depth")
				res, err := engine.Deps(ctx, target, depth)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatDeps(res)), nil

			case "hierarchy":
				symbol := strings.TrimSpace(stringArg(args, "symbol"))
				if symbol == "" {
					return tool.Result{}, fmt.Errorf("symbol is required for hierarchy")
				}
				res, err := engine.Hierarchy(ctx, symbol, stringArg(args, "file"))
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatHierarchy(res)), nil

			case "snippet":
				symbol := strings.TrimSpace(stringArg(args, "symbol"))
				if symbol == "" {
					return tool.Result{}, fmt.Errorf("symbol is required for snippet")
				}
				snip, err := engine.Snippet(ctx, symbol, stringArg(args, "file"))
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(fmt.Sprintf("%s\n%s", nodeLabel(snip.Node), snip.Code) + othersNote(snip.Others)), nil

			case "tests":
				symbol := strings.TrimSpace(stringArg(args, "symbol"))
				if symbol == "" {
					return tool.Result{}, fmt.Errorf("symbol is required for tests")
				}
				res, err := engine.Tests(ctx, symbol, stringArg(args, "file"))
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatTests(res)), nil

			case "co_changes":
				file := strings.TrimSpace(stringArg(args, "file"))
				if file == "" {
					return tool.Result{}, fmt.Errorf("file is required for co_changes")
				}
				limit, _ := tool.IntArg(args, "limit")
				res, err := engine.CoChanges(ctx, file, limit)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatCoChanges(res)), nil

			case "find_similar":
				symbol := strings.TrimSpace(stringArg(args, "symbol"))
				if symbol == "" {
					return tool.Result{}, fmt.Errorf("symbol is required for find_similar")
				}
				limit, _ := tool.IntArg(args, "limit")
				res, err := engine.Similar(ctx, symbol, stringArg(args, "file"), limit)
				if err != nil {
					return tool.Result{}, err
				}
				return tool.Text(formatSimilar(res)), nil

			default:
				return tool.Result{}, fmt.Errorf("operation must be one of: index, status, search, search_content, trace, architecture, dead_code, changes, deps, hierarchy, snippet, tests, co_changes, find_similar")
			}
		},
	}
}

func stringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func boolArg(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}

func edgeBreakdown(stats map[graph.Provenance]int) string {
	lsp := stats[graph.ViaLSP]
	name := stats[graph.ViaName]
	amb := stats[graph.ViaAmbiguous]
	if lsp+name+amb == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d precise, %d name, %d ambiguous)", lsp, name, amb)
}

func formatCoverage(skipped []graph.CoverageIssue) string {
	if len(skipped) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nCoverage: %d source file(s) could not be structurally indexed", len(skipped))
	shown := skipped
	if len(shown) > 5 {
		shown = shown[:5]
	}
	for _, issue := range shown {
		fmt.Fprintf(&b, "\n- %s (%s)", issue.File, issue.Reason)
	}
	if len(skipped) > len(shown) {
		fmt.Fprintf(&b, "\n- … and %d more", len(skipped)-len(shown))
	}
	b.WriteString("\nContent search still scans these files and reports unmatched lines as raw hits.")
	return b.String()
}

// maxListItems caps how many entries any single rendered list shows, so a
// high-degree symbol (a popular interface, a widely-imported package) can't
// flood the agent's context. The full count is always reported in the header.
const maxListItems = 40

func nodeLabel(n *graph.Node) string {
	return fmt.Sprintf("%s (%s) — %s:%d", n.Name, n.Kind, n.File, n.StartLine)
}

func formatNodes(nodes []*graph.Node) string {
	if len(nodes) == 0 {
		return "No matching symbols."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d symbol(s):\n", len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "- %s\n", nodeLabel(n))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSearchResult(res graph.SearchResult) string {
	if res.Total == 0 {
		return "No matching symbols."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Showing %d of %d symbol(s) (offset %d):\n", len(res.Nodes), res.Total, res.Offset)
	for _, n := range res.Nodes {
		fmt.Fprintf(&b, "- %s\n", nodeLabel(n))
	}
	if res.HasMore {
		fmt.Fprintf(&b, "\nMore results available; call again with `offset=%d`.", res.Offset+len(res.Nodes))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatContentSearch(res graph.ContentSearchResult) string {
	if res.TotalResults == 0 && res.TotalRawResults == 0 {
		return "No matching source lines."
	}
	var b strings.Builder
	if res.TotalResults > 0 {
		fmt.Fprintf(&b, "Showing %d of %d containing symbol(s) (offset %d):\n", len(res.Hits), res.TotalResults, res.Offset)
		for _, hit := range res.Hits {
			lines := make([]string, len(hit.MatchLines))
			for i, line := range hit.MatchLines {
				lines[i] = strconv.Itoa(line)
			}
			fmt.Fprintf(&b, "- %s — matches %s; %d caller(s), %d callee(s)\n",
				nodeLabel(hit.Node), strings.Join(lines, ","), hit.Callers, hit.Callees)
		}
		if res.HasMore {
			fmt.Fprintf(&b, "\nMore symbols available; call again with `offset=%d`.\n", res.Offset+len(res.Hits))
		}
	}
	if len(res.Raw) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Raw matches outside indexed definitions (%d of %d):\n", len(res.Raw), res.TotalRawResults)
		for _, hit := range res.Raw {
			fmt.Fprintf(&b, "- %s:%d: %s\n", hit.File, hit.Line, hit.Content)
		}
		if res.RawHasMore {
			b.WriteString("- … more raw matches omitted; narrow with `file` or `glob`\n")
		}
	}
	fmt.Fprintf(&b, "\nTotal matching source lines: %d.", res.TotalLineHits)
	return strings.TrimRight(b.String(), "\n")
}

func formatTrace(res graph.TraceResult, callers bool) string {
	verb := "calls"
	if callers {
		verb = "called by"
	}

	if len(res.Paths) == 0 {
		return fmt.Sprintf("No %s relationships found.", strings.TrimSuffix(verb, " by"))
	}

	ambiguous := false
	var b strings.Builder
	if len(res.Roots) > 1 {
		fmt.Fprintf(&b, "%d definitions share this name — paths merge all of them (pass `file` to pick one):\n", len(res.Roots))
		for _, r := range res.Roots {
			fmt.Fprintf(&b, "- %s\n", nodeLabel(r))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d path(s) (%s):\n", len(res.Paths), verb)
	paths := res.Paths
	if len(paths) > maxListItems {
		paths = paths[:maxListItems]
	}
	for _, p := range paths {
		var line strings.Builder
		for i, n := range p.Nodes {
			if i > 0 {
				if i-1 < len(p.Via) && p.Via[i-1] == graph.ViaAmbiguous {
					line.WriteString(" ⇢ ")
					ambiguous = true
				} else {
					line.WriteString(" → ")
				}
			}
			line.WriteString(n.Name)
		}
		last := p.Nodes[len(p.Nodes)-1]
		fmt.Fprintf(&b, "- %s   [%s:%d]\n", line.String(), last.File, last.StartLine)
	}
	if len(res.Paths) > len(paths) {
		fmt.Fprintf(&b, "  … and %d more path(s); narrow with `target` or a smaller `depth`\n", len(res.Paths)-len(paths))
	}
	if ambiguous {
		b.WriteString("\n⇢ = ambiguous name-based hop (install a language server to resolve precisely)")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatDeps(d graph.DepsResult) string {
	if len(d.DependsOn)+len(d.DependedBy)+len(d.External) == 0 {
		return fmt.Sprintf("Module %q has no recorded imports or importers.", d.Module)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Module: %s", d.Module)
	writeDepList(&b, "Depends on (local)", d.DependsOn)
	writeDepList(&b, "Depended on by (local)", d.DependedBy)
	writeDepList(&b, "Transitive (indirect) deps", d.Transitive)
	writeDepList(&b, "External imports", d.External)
	return strings.TrimRight(b.String(), "\n")
}

func writeDepList(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n\n%s (%d):\n", title, len(items))
	shown := items
	if len(shown) > maxListItems {
		shown = shown[:maxListItems]
	}
	for _, it := range shown {
		fmt.Fprintf(b, "- %s\n", it)
	}
	if len(items) > len(shown) {
		fmt.Fprintf(b, "  … and %d more\n", len(items)-len(shown))
	}
}

func formatHierarchy(h graph.HierarchyResult) string {
	if h.Type == nil {
		return "Type not found."
	}
	total := len(h.Extends) + len(h.Subtypes) + len(h.Implements) + len(h.Implementers)
	if total == 0 {
		return fmt.Sprintf("%s\n(no recorded type-hierarchy relationships)", nodeLabel(h.Type))
	}
	var b strings.Builder
	fmt.Fprint(&b, nodeLabel(h.Type))
	writeNodeList(&b, "Extends", h.Extends)
	writeNodeList(&b, "Subtypes (extended/embedded by)", h.Subtypes)
	writeNodeList(&b, "Implements", h.Implements)
	writeNodeList(&b, "Implemented by", h.Implementers)
	return strings.TrimRight(b.String(), "\n") + othersNote(h.Others)
}

func writeNodeList(b *strings.Builder, title string, nodes []*graph.Node) {
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintf(b, "\n\n%s (%d):\n", title, len(nodes))
	shown := nodes
	if len(shown) > maxListItems {
		shown = shown[:maxListItems]
	}
	for _, n := range shown {
		fmt.Fprintf(b, "- %s\n", nodeLabel(n))
	}
	if len(nodes) > len(shown) {
		fmt.Fprintf(b, "  … and %d more\n", len(nodes)-len(shown))
	}
}

func formatTests(res graph.TestsResult) string {
	if res.Symbol == nil {
		return "Symbol not found."
	}
	if len(res.TestedBy) == 0 && len(res.Covers) == 0 {
		return fmt.Sprintf("%s\n(no test relationships detected — direct calls into/out of test files only)", nodeLabel(res.Symbol))
	}
	var b strings.Builder
	fmt.Fprint(&b, nodeLabel(res.Symbol))
	writeNodeList(&b, "Tested by", res.TestedBy)
	writeNodeList(&b, "Covers", res.Covers)
	return strings.TrimRight(b.String(), "\n") + othersNote(res.Others)
}

func formatSimilar(res graph.SimilarResult) string {
	if res.Target == nil {
		return "Symbol not found."
	}
	if len(res.Matches) == 0 {
		return fmt.Sprintf("%s\n(no similar functions found)", nodeLabel(res.Target))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Similar to %s:\n", nodeLabel(res.Target))
	for _, m := range res.Matches {
		fmt.Fprintf(&b, "- %s  (%.2f)\n", nodeLabel(m.Node), m.Score)
	}
	return strings.TrimRight(b.String(), "\n") + othersNote(res.Others)
}

func formatCoChanges(res graph.CoChangesResult) string {
	if res.Commits == 0 {
		return fmt.Sprintf("No git history found touching %q. Pass an exact repo-relative path (not a substring), and check this is a git repo.", res.File)
	}
	if len(res.Related) == 0 {
		return fmt.Sprintf("%s changed in %d commit(s), always alone.", res.File, res.Commits)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Files changing with %s (across %d commit(s)):\n", res.File, res.Commits)
	for _, c := range res.Related {
		fmt.Fprintf(&b, "- %s (%d×)\n", c.File, c.Count)
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatChanges(changes graph.Changes) string {
	if len(changes.Files) == 0 {
		return "No changes detected."
	}
	var b strings.Builder
	for _, f := range changes.Files {
		fmt.Fprintf(&b, "%s (%s):\n", f.File, f.Kind)
		if len(f.Nodes) == 0 {
			b.WriteString("  (no tracked definitions affected)\n")
			continue
		}
		for _, n := range f.Nodes {
			fmt.Fprintf(&b, "  - %s\n", nodeLabel(n))
		}
	}

	if len(changes.Impact) > 0 {
		fmt.Fprintf(&b, "\nImpact — unchanged callers of changed definitions (%d):\n", len(changes.Impact))
		shown := changes.Impact
		if len(shown) > maxListItems {
			shown = shown[:maxListItems]
		}
		for _, imp := range shown {
			names := make([]string, len(imp.Calls))
			for i, c := range imp.Calls {
				names[i] = c.Name
			}
			fmt.Fprintf(&b, "- %s — calls %s\n", nodeLabel(imp.Caller), strings.Join(names, ", "))
		}
		if len(changes.Impact) > len(shown) {
			fmt.Fprintf(&b, "  … and %d more caller(s)\n", len(changes.Impact)-len(shown))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func othersNote(others []*graph.Node) string {
	if len(others) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n%d other definition(s) share this name (pass `file` or qualify the symbol to select):\n", len(others))
	shown := others
	if len(shown) > 5 {
		shown = shown[:5]
	}
	for _, n := range shown {
		fmt.Fprintf(&b, "- %s\n", nodeLabel(n))
	}
	if len(others) > len(shown) {
		fmt.Fprintf(&b, "  … and %d more\n", len(others)-len(shown))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatArch(arch graph.Arch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Files: %d  Definitions: %d  Call edges: %d\n\n", arch.TotalFiles, arch.TotalNodes, arch.TotalEdges)

	b.WriteString("Languages:\n")
	for _, l := range arch.Languages {
		fmt.Fprintf(&b, "- %s: %d files, %d defs\n", l.Lang, l.Files, l.Nodes)
	}

	if len(arch.Layers) > 0 {
		b.WriteString("\nLayers (top-level):\n")
		for _, l := range arch.Layers {
			fmt.Fprintf(&b, "- %s/: %d files, %d defs\n", l.Path, l.Files, l.Nodes)
		}
	}

	if len(arch.Modules) > 0 {
		b.WriteString("\nModules (top by size):\n")
		for _, m := range arch.Modules {
			fmt.Fprintf(&b, "- %s: %d files, %d defs\n", m.Path, m.Files, m.Nodes)
		}
	}

	if len(arch.ModuleDeps) > 0 {
		b.WriteString("\nMost-depended-on modules:\n")
		for _, m := range arch.ModuleDeps {
			fmt.Fprintf(&b, "- %s: %d dependents, %d dependencies\n", m.Module, m.DependedBy, m.DependsOn)
		}
	}

	if len(arch.EntryPoints) > 0 {
		b.WriteString("\nEntry points:\n")
		for _, n := range arch.EntryPoints {
			fmt.Fprintf(&b, "- %s\n", nodeLabel(n))
		}
	}

	if len(arch.Hotspots) > 0 {
		b.WriteString("\nHotspots (most connected):\n")
		for _, h := range arch.Hotspots {
			fmt.Fprintf(&b, "- %s — %d callers, %d callees\n", nodeLabel(h.Node), h.Callers, h.Callees)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
