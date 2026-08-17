package graph

import (
	"context"
	"path"
	"sort"
)

type ModuleGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ModuleGraph struct {
	Modules []ModuleStat      `json:"modules"`
	Edges   []ModuleGraphEdge `json:"edges"`
}

func (e *Engine) ModuleGraph(ctx context.Context) (ModuleGraph, error) {
	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return ModuleGraph{}, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return g.moduleGraph(), nil
}

func (g *Graph) moduleGraph() ModuleGraph {
	stats := groupByPath(g.Nodes, path.Dir)

	present := make(map[string]bool, len(stats))
	for _, m := range stats {
		present[m.Path] = true
	}

	var edges []ModuleGraphEdge
	for from, tos := range g.modOut {
		for to := range tos {
			if !present[from] || !present[to] {
				continue
			}
			edges = append(edges, ModuleGraphEdge{From: from, To: to})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	return ModuleGraph{Modules: stats, Edges: edges}
}

type NeighborhoodResult struct {
	Node         *Node   `json:"node"`
	Callers      []*Node `json:"callers"`
	Callees      []*Node `json:"callees"`
	Extends      []*Node `json:"extends,omitempty"`
	Subtypes     []*Node `json:"subtypes,omitempty"`
	Implements   []*Node `json:"implements,omitempty"`
	Implementers []*Node `json:"implementers,omitempty"`
	Others       []*Node `json:"others,omitempty"`
}

func (e *Engine) Neighborhood(ctx context.Context, id, name, file string) (NeighborhoodResult, error) {
	g, err := e.ensureIndexed(ctx)
	if err != nil {
		return NeighborhoodResult{}, err
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	var node *Node
	var others []*Node
	if id != "" {
		node = g.byID[id]
	}
	if node == nil {
		cands := g.resolve(name, file)
		if len(cands) == 0 {
			return NeighborhoodResult{}, notFoundErr(name, file)
		}
		node = cands[0]
		others = othersOf(cands, node)
	}

	return NeighborhoodResult{
		Node:         node,
		Callers:      g.nodesFor(g.in[node.ID]),
		Callees:      g.nodesFor(g.out[node.ID]),
		Extends:      g.nodesFor(g.superOut[node.ID]),
		Subtypes:     g.nodesFor(g.superIn[node.ID]),
		Implements:   g.nodesFor(g.implOut[node.ID]),
		Implementers: g.nodesFor(g.implIn[node.ID]),
		Others:       others,
	}, nil
}
