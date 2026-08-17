package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/adrianliechti/wingman-agent/pkg/graph"
)

type graphStatus struct {
	Indexed   bool                  `json:"indexed"`
	IndexedAt time.Time             `json:"indexed_at"`
	Stale     bool                  `json:"stale"`
	Files     int                   `json:"files"`
	Nodes     int                   `json:"nodes"`
	Edges     int                   `json:"edges"`
	Skipped   []graph.CoverageIssue `json:"skipped,omitempty"`
}

type graphOverview struct {
	Status graphStatus `json:"status"`
	Arch   graph.Arch  `json:"arch"`
}

func (s *Server) graphEngine(w http.ResponseWriter) *graph.Engine {
	engine, err := s.workspace.GraphEngine()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return nil
	}
	return engine
}

func (s *Server) handleGraphOverview(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	arch, err := engine.Architecture(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := engine.Status()
	writeJSON(w, graphOverview{
		Status: graphStatus{
			Indexed:   status.Indexed,
			IndexedAt: status.IndexedAt,
			Stale:     engine.IsStale(r.Context()),
			Files:     status.Files,
			Nodes:     status.Nodes,
			Edges:     status.Edges,
			Skipped:   status.Skipped,
		},
		Arch: arch,
	})
}

func (s *Server) handleGraphIndex(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}
	status, err := engine.Index(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, graphStatus{
		Indexed:   status.Indexed,
		IndexedAt: status.IndexedAt,
		Files:     status.Files,
		Nodes:     status.Nodes,
		Edges:     status.Edges,
		Skipped:   status.Skipped,
	})
}

func (s *Server) handleGraphSearch(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	offset, _ := strconv.Atoi(query.Get("offset"))

	result, err := engine.SearchPage(r.Context(), graph.SearchOpts{
		Query:  query.Get("q"),
		Kind:   graph.Kind(query.Get("kind")),
		File:   query.Get("file"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.Nodes == nil {
		result.Nodes = []*graph.Node{}
	}
	writeJSON(w, result)
}

type graphContentSearchRequest struct {
	Pattern    string `json:"pattern"`
	Regex      bool   `json:"regex"`
	IgnoreCase bool   `json:"ignore_case"`
	File       string `json:"file"`
	Glob       string `json:"glob"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

func (s *Server) handleGraphContentSearch(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	var request graphContentSearchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	result, err := engine.SearchContent(r.Context(), graph.ContentSearchOpts{
		Pattern:    request.Pattern,
		Regex:      request.Regex,
		IgnoreCase: request.IgnoreCase,
		File:       request.File,
		Glob:       request.Glob,
		Limit:      request.Limit,
		Offset:     request.Offset,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if result.Hits == nil {
		result.Hits = []graph.ContentHit{}
	}
	writeJSON(w, result)
}

func (s *Server) handleGraphSymbol(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	query := r.URL.Query()
	id := query.Get("id")
	name := query.Get("name")
	if id == "" && name == "" {
		http.Error(w, "id or name is required", http.StatusBadRequest)
		return
	}

	result, err := engine.Neighborhood(r.Context(), id, name, query.Get("file"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleGraphInsights(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	result, err := engine.GitInsights(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if result.Weeks == nil {
		result.Weeks = []graph.WeekActivity{}
	}
	if result.AuthorWeeks == nil {
		result.AuthorWeeks = []graph.AuthorSeries{}
	}
	if result.Authors == nil {
		result.Authors = []graph.AuthorStat{}
	}
	if result.Modules == nil {
		result.Modules = []graph.ModuleActivity{}
	}
	if result.Churn == nil {
		result.Churn = []graph.ChurnStat{}
	}
	writeJSON(w, result)
}

func (s *Server) handleGraphModules(w http.ResponseWriter, r *http.Request) {
	engine := s.graphEngine(w)
	if engine == nil {
		return
	}

	result, err := engine.ModuleGraph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.Modules == nil {
		result.Modules = []graph.ModuleStat{}
	}
	if result.Edges == nil {
		result.Edges = []graph.ModuleGraphEdge{}
	}
	writeJSON(w, result)
}
