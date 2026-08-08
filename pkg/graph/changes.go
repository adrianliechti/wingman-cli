package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

type fileChange struct {
	Path     string
	Kind     ChangeKind
	Lines    []int
	AllLines bool
}

type AffectedFile struct {
	File  string     `json:"file"`
	Kind  ChangeKind `json:"kind"`
	Nodes []*Node    `json:"nodes"`
}

type Impact struct {
	Caller *Node   `json:"caller"`
	Calls  []*Node `json:"calls"`
}

type Changes struct {
	Files  []AffectedFile `json:"files"`
	Impact []Impact       `json:"impact,omitempty"`
}

// gitChanges diffs the working tree against a base tree: HEAD by default, or
// the resolved `since` revision. With `since`, committed changes (base..HEAD)
// are included alongside uncommitted ones.
func gitChanges(root, since string) ([]fileChange, error) {
	repo, err := git.PlainOpen(root)
	if err != nil {
		return nil, err
	}

	wt, err := repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	var baseTree *object.Tree
	if since != "" {
		hash, err := repo.ResolveRevision(plumbing.Revision(since))
		if err != nil {
			return nil, fmt.Errorf("cannot resolve git revision %q: %w", since, err)
		}
		commit, err := repo.CommitObject(*hash)
		if err != nil {
			return nil, fmt.Errorf("revision %q is not a commit: %w", since, err)
		}
		if baseTree, err = commit.Tree(); err != nil {
			return nil, err
		}
	} else if ref, err := repo.Head(); err == nil {
		if commit, err := repo.CommitObject(ref.Hash()); err == nil {
			baseTree, _ = commit.Tree()
		}
	}

	paths := map[string]bool{}
	for path := range status {
		paths[path] = true
	}
	if since != "" {
		committed, err := committedPaths(repo, baseTree)
		if err != nil {
			return nil, err
		}
		for path := range committed {
			paths[path] = true
		}
	}

	var out []fileChange
	for path := range paths {
		slashPath := filepath.ToSlash(path)

		var baseContent string
		inBase := false
		if baseTree != nil {
			if f, err := baseTree.File(path); err == nil {
				if c, err := f.Contents(); err == nil {
					baseContent = c
					inBase = true
				}
			}
		}

		diskBytes, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		onDisk := readErr == nil

		switch {
		case !inBase && onDisk:
			out = append(out, fileChange{Path: slashPath, Kind: ChangeAdded, AllLines: true})
		case inBase && !onDisk:
			out = append(out, fileChange{Path: slashPath, Kind: ChangeDeleted})
		case inBase && onDisk:
			lines := changedNewLines(baseContent, string(diskBytes))
			if len(lines) == 0 {
				continue
			}
			out = append(out, fileChange{Path: slashPath, Kind: ChangeModified, Lines: lines})
		}
	}

	return out, nil
}

func committedPaths(repo *git.Repository, baseTree *object.Tree) (map[string]bool, error) {
	out := map[string]bool{}

	ref, err := repo.Head()
	if err != nil {
		return out, nil
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, err
	}
	headTree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	diff, err := object.DiffTree(baseTree, headTree)
	if err != nil {
		return nil, err
	}
	for _, ch := range diff {
		if ch.From.Name != "" {
			out[ch.From.Name] = true
		}
		if ch.To.Name != "" {
			out[ch.To.Name] = true
		}
	}
	return out, nil
}

func changedNewLines(oldText, newText string) []int {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)

	var changed []int
	lineNo := 0
	for _, d := range diffs {
		n := countLines(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			lineNo += n
		case diffmatchpatch.DiffInsert:
			for range n {
				lineNo++
				changed = append(changed, lineNo)
			}
		case diffmatchpatch.DiffDelete:
			if lineNo >= 1 {
				changed = append(changed, lineNo)
			}
		}
	}
	return changed
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func affectedNodes(g *Graph, changes []fileChange) Changes {
	var result Changes

	for _, c := range changes {
		af := AffectedFile{File: c.Path, Kind: c.Kind}

		if c.Kind != ChangeDeleted {
			nodeset := map[string]*Node{}
			fileNodes := g.byFile[c.Path]

			if c.AllLines {
				for _, n := range fileNodes {
					nodeset[n.ID] = n
				}
			} else {
				lines := append([]int(nil), c.Lines...)
				sort.Ints(lines)
				for _, n := range fileNodes {
					i := sort.SearchInts(lines, n.StartLine)
					if i < len(lines) && lines[i] <= n.EndLine {
						nodeset[n.ID] = n
					}
				}
			}

			for _, n := range nodeset {
				af.Nodes = append(af.Nodes, n)
			}
			sort.SliceStable(af.Nodes, func(i, j int) bool {
				return af.Nodes[i].StartLine < af.Nodes[j].StartLine
			})
		}

		result.Files = append(result.Files, af)
	}

	sort.SliceStable(result.Files, func(i, j int) bool {
		return result.Files[i].File < result.Files[j].File
	})

	result.Impact = impactedCallers(g, result.Files)
	return result
}

// impactedCallers lists unchanged callers of changed definitions — the blast
// radius of the edit one call level out.
func impactedCallers(g *Graph, files []AffectedFile) []Impact {
	changed := map[string]bool{}
	for _, f := range files {
		for _, n := range f.Nodes {
			changed[n.ID] = true
		}
	}

	callerCalls := map[string]map[string]bool{}
	for _, f := range files {
		for _, n := range f.Nodes {
			for _, callerID := range g.in[n.ID] {
				if changed[callerID] || g.byID[callerID] == nil {
					continue
				}
				if callerCalls[callerID] == nil {
					callerCalls[callerID] = map[string]bool{}
				}
				callerCalls[callerID][n.ID] = true
			}
		}
	}

	var out []Impact
	for callerID, callIDs := range callerCalls {
		imp := Impact{Caller: g.byID[callerID]}
		for id := range callIDs {
			imp.Calls = append(imp.Calls, g.byID[id])
		}
		sort.SliceStable(imp.Calls, func(i, j int) bool {
			return imp.Calls[i].Name < imp.Calls[j].Name
		})
		out = append(out, imp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Calls) != len(out[j].Calls) {
			return len(out[i].Calls) > len(out[j].Calls)
		}
		if out[i].Caller.File != out[j].Caller.File {
			return out[i].Caller.File < out[j].Caller.File
		}
		return out[i].Caller.Name < out[j].Caller.Name
	})
	return out
}
