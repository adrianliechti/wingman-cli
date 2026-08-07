package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// snapshot persists the graph. Edges reference nodes by their index in Nodes
// rather than by their full string ID, so the on-disk size is dominated by the
// nodes instead of by ID strings repeated twice per edge — on large repos the
// edge list otherwise dwarfs everything else.
type snapshot struct {
	Version      int                 `json:"version"`
	IndexedAt    time.Time           `json:"indexed_at"`
	Files        map[string]fileMeta `json:"files"`
	IndexedFiles int                 `json:"indexed_files"`
	Skipped      []CoverageIssue     `json:"skipped,omitempty"`
	Nodes        []*Node             `json:"nodes"`
	Edges        []edgeRec           `json:"edges"`
	Imports      []*Import           `json:"imports,omitempty"`
}

type edgeRec struct {
	From int32      `json:"f"`
	To   int32      `json:"t"`
	Kind EdgeKind   `json:"k"`
	Via  Provenance `json:"v,omitempty"`
}

const snapshotVersion = 4

func loadSnapshot(path string) (*Graph, map[string]fileMeta, int, []CoverageIssue, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, 0, nil, time.Time{}, err
	}

	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, nil, 0, nil, time.Time{}, err
	}
	if snap.Version != snapshotVersion {
		return nil, nil, 0, nil, time.Time{}, os.ErrNotExist
	}

	g := &Graph{Nodes: snap.Nodes, Imports: snap.Imports}
	g.Edges = make([]*Edge, 0, len(snap.Edges))
	for _, e := range snap.Edges {
		if int(e.From) >= len(snap.Nodes) || int(e.To) >= len(snap.Nodes) || e.From < 0 || e.To < 0 {
			continue
		}
		g.Edges = append(g.Edges, &Edge{
			From: snap.Nodes[e.From].ID,
			To:   snap.Nodes[e.To].ID,
			Kind: e.Kind,
			Via:  e.Via,
		})
	}
	g.build()
	return g, snap.Files, snap.IndexedFiles, snap.Skipped, snap.IndexedAt, nil
}

func saveSnapshot(path string, g *Graph, files map[string]fileMeta, indexedFiles int, skipped []CoverageIssue, indexedAt time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	idx := make(map[string]int32, len(g.Nodes))
	for i, n := range g.Nodes {
		idx[n.ID] = int32(i)
	}

	edges := make([]edgeRec, 0, len(g.Edges))
	for _, e := range g.Edges {
		from, ok1 := idx[e.From]
		to, ok2 := idx[e.To]
		if !ok1 || !ok2 {
			continue
		}
		edges = append(edges, edgeRec{From: from, To: to, Kind: e.Kind, Via: e.Via})
	}

	snap := snapshot{
		Version:      snapshotVersion,
		IndexedAt:    indexedAt,
		Files:        files,
		IndexedFiles: indexedFiles,
		Skipped:      skipped,
		Nodes:        g.Nodes,
		Edges:        edges,
		Imports:      g.Imports,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
