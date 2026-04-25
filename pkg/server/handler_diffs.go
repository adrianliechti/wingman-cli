package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/app/rewind"
)

func (s *Server) handleDiffs(w http.ResponseWriter, r *http.Request) {
	// Check if rewind is ready
	select {
	case <-s.rewindReady:
	default:
		http.Error(w, "rewind not ready", http.StatusServiceUnavailable)
		return
	}

	if s.rewind == nil {
		writeJSON(w, []DiffEntry{})
		return
	}

	diffs, err := s.rewind.DiffFromBaseline()
	if err != nil {
		writeJSON(w, []DiffEntry{})
		return
	}

	var result []DiffEntry

	for _, d := range diffs {
		status := "modified"
		switch d.Status {
		case rewind.StatusAdded:
			status = "added"
		case rewind.StatusDeleted:
			status = "deleted"
		}

		result = append(result, DiffEntry{
			Path:     d.Path,
			Status:   status,
			Patch:    d.Patch,
			Original: d.Original,
			Modified: d.Modified,
			Language: extToLanguage[strings.ToLower(filepath.Ext(d.Path))],
		})
	}

	if result == nil {
		result = []DiffEntry{}
	}

	writeJSON(w, result)
}
