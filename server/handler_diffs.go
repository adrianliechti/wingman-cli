package server

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/changes"
)

func (s *Server) handleDiffs(w http.ResponseWriter, r *http.Request) {
	onlyPath := r.URL.Query().Get("path")
	if onlyPath != "" {
		rel, ok := s.workspaceRel(onlyPath)
		if !ok {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		onlyPath = filepath.ToSlash(rel)
	}
	layer := changes.DiffLayer(r.URL.Query().Get("layer"))
	if layer != changes.DiffCombined && layer != changes.DiffStaged && layer != changes.DiffUnstaged {
		http.Error(w, "invalid diff layer", http.StatusBadRequest)
		return
	}

	var diffs []changes.FileDiff
	var err error
	if onlyPath != "" && layer != changes.DiffCombined {
		var diff changes.FileDiff
		diff, err = s.workspace.Diff(r.Context(), onlyPath, layer)
		if err == nil {
			diffs = []changes.FileDiff{diff}
		}
	} else {
		diffs, err = s.workspace.Diffs(r.Context())
	}
	if err != nil {
		if errors.Is(err, changes.ErrNoDiff) {
			writeJSON(w, []DiffEntry{})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]DiffEntry, 0, len(diffs))
	for _, d := range diffs {
		if onlyPath != "" && d.Path != onlyPath {
			continue
		}
		result = append(result, diffEntry(d))
	}

	writeJSON(w, result)
}

func diffEntry(d changes.FileDiff) DiffEntry {
	status := "modified"
	switch d.Status {
	case changes.StatusAdded:
		status = "added"
	case changes.StatusDeleted:
		status = "deleted"
	}
	return DiffEntry{
		Path:         d.Path,
		OriginalPath: d.OriginalPath,
		Status:       status,
		Patch:        d.Patch,
		Original:     d.Original,
		Modified:     d.Modified,
		Language:     extToLanguage[strings.ToLower(filepath.Ext(d.Path))],
	}
}

func (s *Server) handleDiffRevert(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.workspaceRel(r.URL.Query().Get("path"))
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	canonical := filepath.ToSlash(rel)
	if err := s.workspace.RevertChange(r.Context(), canonical); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.flushFiles()
	s.broadcast(Frame{Type: EvtDiffsChanged})
	w.WriteHeader(http.StatusNoContent)
}
