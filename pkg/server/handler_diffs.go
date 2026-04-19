package server

import (
	"net/http"

	"github.com/adrianliechti/wingman-agent/pkg/agent/rewind"
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
			Path:   d.Path,
			Status: status,
			Patch:  d.Patch,
		})
	}

	if result == nil {
		result = []DiffEntry{}
	}

	writeJSON(w, result)
}
