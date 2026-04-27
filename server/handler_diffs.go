package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrianliechti/wingman-agent/pkg/rewind"
)

func (s *Server) handleDiffs(w http.ResponseWriter, r *http.Request) {
	if s.agent.Rewind == nil {
		writeJSON(w, []DiffEntry{})
		return
	}

	diffs, err := s.agent.Rewind.DiffFromBaseline()
	if err != nil {
		// Real git failure (corrupt baseline, snapshot failed, …). Surface it
		// to stderr so it's actually visible; the panel still renders empty.
		fmt.Fprintf(os.Stderr, "diffs: %v\n", err)
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
