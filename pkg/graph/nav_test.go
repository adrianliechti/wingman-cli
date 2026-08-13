package graph

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func newNavEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "main.go", `package main

func main() {
	greet("world")
}
`)
	writeFile(t, root, "lib/greet.go", `package lib

func greet(name string) string {
	return "hi " + name
}
`)
	writeFile(t, root, "web/app.ts", `export class Animal {
	speak(): string {
		return makeNoise();
	}
}

export class Dog extends Animal {}

function makeNoise(): string {
	return "woof";
}

export function greet(): string {
	return "hello";
}
`)
	writeFile(t, root, "web/view.tsx", `import { Animal } from "./app";

export class Cat extends Animal {}
`)

	cache := filepath.Join(t.TempDir(), "graph.json")
	return New(root, cache)
}

func TestDefinitionsFromCallSite(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Definitions(context.Background(), "main.go", nil, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 {
		t.Fatalf("locations = %+v, want only the Go definition despite the TS greet", locs)
	}
	if locs[0].File != "lib/greet.go" || locs[0].Line != 2 {
		t.Fatalf("location = %+v, want lib/greet.go line 2", locs[0])
	}
	if locs[0].Col != 5 {
		t.Fatalf("column = %d, want 5 (start of name)", locs[0].Col)
	}
}

func TestDefinitionsAtEndOfWord(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Definitions(context.Background(), "main.go", nil, 3, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].File != "lib/greet.go" {
		t.Fatalf("locations = %+v, want lib/greet.go", locs)
	}
}

func TestDefinitionsAcrossLangFamily(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Definitions(context.Background(), "web/view.tsx", nil, 2, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 || locs[0].File != "web/app.ts" || locs[0].Line != 0 {
		t.Fatalf("locations = %+v, want class Animal in web/app.ts", locs)
	}
}

func TestDefinitionsPrefersSameFile(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Definitions(context.Background(), "web/app.ts", nil, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 || locs[0].File != "web/app.ts" {
		t.Fatalf("locations = %+v, want web/app.ts first", locs)
	}
}

func TestDefinitionsUsesProvidedContent(t *testing.T) {
	e := newNavEngine(t)

	edited := []byte("package main\n\nfunc main() {\n\tx := greet\n\t_ = x\n}\n")
	locs, err := e.Definitions(context.Background(), "main.go", edited, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 1 || locs[0].File != "lib/greet.go" {
		t.Fatalf("locations = %+v, want lib/greet.go", locs)
	}
}

func TestDefinitionsNoIdentifier(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Definitions(context.Background(), "main.go", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 0 {
		t.Fatalf("locations = %+v, want none on keyword", locs)
	}
}

func TestReferencesIncludeDeclarationAndCallSites(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.References(context.Background(), "lib/greet.go", nil, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	var haveDecl, haveCall bool
	for _, l := range locs {
		if l.File == "lib/greet.go" && l.Line == 2 {
			haveDecl = true
		}
		if l.File == "main.go" && l.Line == 3 {
			haveCall = true
		}
	}
	if !haveDecl || !haveCall {
		t.Fatalf("locations = %+v, want declaration and call site", locs)
	}
}

func TestReferencesFindHierarchySites(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.References(context.Background(), "web/app.ts", nil, 0, 14)
	if err != nil {
		t.Fatal(err)
	}
	var haveExtends bool
	for _, l := range locs {
		if l.File == "web/app.ts" && l.Line == 6 {
			haveExtends = true
		}
	}
	if !haveExtends {
		t.Fatalf("locations = %+v, want extends clause on line 6", locs)
	}
}

func TestHoverShowsDefinitionSnippet(t *testing.T) {
	e := newNavEngine(t)

	info, err := e.Hover(context.Background(), "main.go", nil, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Node.File != "lib/greet.go" {
		t.Fatalf("info = %+v, want lib/greet.go", info)
	}
	if !strings.Contains(info.Code, "func greet(name string) string") {
		t.Fatalf("code = %q, want signature", info.Code)
	}
	if info.Others != 0 {
		t.Fatalf("others = %d, want 0 (TS greet is another family)", info.Others)
	}
}

func TestHoverNoMatch(t *testing.T) {
	e := newNavEngine(t)

	info, err := e.Hover(context.Background(), "main.go", nil, 0, 0)
	if err != nil || info != nil {
		t.Fatalf("info = %+v, err = %v, want nil on keyword", info, err)
	}
}

func TestImplementationsFindSubtypes(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Implementations(context.Background(), "web/app.ts", nil, 0, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Fatalf("locations = %+v, want Dog and Cat", locs)
	}
	if locs[0].File != "web/app.ts" || locs[0].Line != 6 {
		t.Fatalf("first = %+v, want Dog in web/app.ts line 6", locs[0])
	}
	if locs[1].File != "web/view.tsx" || locs[1].Line != 2 {
		t.Fatalf("second = %+v, want Cat in web/view.tsx line 2", locs[1])
	}
}

func TestImplementationsEmptyForFunctions(t *testing.T) {
	e := newNavEngine(t)

	locs, err := e.Implementations(context.Background(), "lib/greet.go", nil, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 0 {
		t.Fatalf("locations = %+v, want none", locs)
	}
}

func TestFileSymbolsNesting(t *testing.T) {
	src := []byte(`export class Animal {
	speak(): string {
		return "";
	}
}

function makeNoise(): string {
	return "woof";
}
`)
	syms := FileSymbols("app.ts", src)
	if len(syms) != 2 {
		t.Fatalf("symbols = %+v, want two roots", syms)
	}
	if syms[0].Name != "Animal" || syms[0].Kind != KindClass {
		t.Fatalf("first = %+v, want class Animal", syms[0])
	}
	if len(syms[0].Children) != 1 || syms[0].Children[0].Name != "speak" {
		t.Fatalf("children = %+v, want speak", syms[0].Children)
	}
	if syms[1].Name != "makeNoise" || syms[1].Kind != KindFunction {
		t.Fatalf("second = %+v, want function makeNoise", syms[1])
	}
	if syms[0].NameRange.StartLine != 0 || syms[0].NameRange.StartCol != 13 {
		t.Fatalf("name range = %+v, want line 0 col 13", syms[0].NameRange)
	}
}

func TestFileSymbolsUnsupportedFile(t *testing.T) {
	if syms := FileSymbols("readme.txt", []byte("hello")); syms != nil {
		t.Fatalf("symbols = %+v, want nil", syms)
	}
}

func TestDocumentHighlightsUseIdentifierNodes(t *testing.T) {
	src := []byte("package main\n\nfunc greet() {\n\tgreet()\n\t_ = \"greet\"\n}\n")
	ranges := DocumentHighlights("main.go", src, 3, 3)
	if len(ranges) != 2 {
		t.Fatalf("highlights = %+v, want declaration and call only", ranges)
	}
	if ranges[0].StartLine != 2 || ranges[1].StartLine != 3 {
		t.Fatalf("highlights = %+v, want lines 2 and 3", ranges)
	}
}

func TestFoldingRangesUseSymbolBodies(t *testing.T) {
	src := []byte("package main\n\nfunc greet() {\n\tprintln(\"hello\")\n}\n")
	ranges := FoldingRanges("main.go", src)
	if len(ranges) != 1 || ranges[0].StartLine != 2 || ranges[0].EndLine != 4 {
		t.Fatalf("folds = %+v, want function body", ranges)
	}
}

func TestSemanticTokensUseHighlightQueries(t *testing.T) {
	src := []byte("package main\n\nfunc greet(name string) string { return \"hi " + name }\n")
	tokens := SemanticTokens("main.go", src)
	if len(tokens) == 0 {
		t.Fatal("expected semantic tokens")
	}
	want := map[string]bool{"keyword": false, "function": false, "string": false}
	for _, token := range tokens {
		if _, ok := want[token.Type]; ok {
			want[token.Type] = true
		}
	}
	for tokenType, found := range want {
		if !found {
			t.Fatalf("semantic tokens = %+v, missing %s", tokens, tokenType)
		}
	}
}

func TestSnapshotRoundtripKeepsRefsAndNamePositions(t *testing.T) {
	e := newNavEngine(t)
	ctx := context.Background()
	if _, err := e.Index(ctx); err != nil {
		t.Fatal(err)
	}

	reloaded := New(e.root, e.cachePath)
	locs, err := reloaded.References(ctx, "lib/greet.go", nil, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	var haveCall bool
	for _, l := range locs {
		if l.File == "main.go" && l.Line == 3 {
			haveCall = true
		}
	}
	if !haveCall {
		t.Fatalf("locations = %+v, want call site from snapshot", locs)
	}
	defs, err := reloaded.Definitions(ctx, "main.go", nil, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Col != 5 {
		t.Fatalf("definitions = %+v, want name column from snapshot", defs)
	}
}
