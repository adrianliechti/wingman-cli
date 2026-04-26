package server

import (
	"net/http"
)

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	select {
	case <-s.rewindReady:
	default:
		writeJSON(w, []CheckpointEntry{})
		return
	}

	if s.rewind == nil {
		writeJSON(w, []CheckpointEntry{})
		return
	}

	checkpoints, err := s.rewind.List()
	if err != nil {
		writeJSON(w, []CheckpointEntry{})
		return
	}

	result := make([]CheckpointEntry, 0, len(checkpoints))
	for _, cp := range checkpoints {
		result = append(result, CheckpointEntry{
			Hash:    cp.Hash,
			Message: cp.Message,
			Time:    cp.Time.Format("2006-01-02 15:04:05"),
		})
	}

	writeJSON(w, result)
}

func (s *Server) handleCheckpointRestore(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		http.Error(w, "hash required", http.StatusBadRequest)
		return
	}

	select {
	case <-s.rewindReady:
	default:
		http.Error(w, "rewind not ready", http.StatusServiceUnavailable)
		return
	}

	if s.rewind == nil {
		http.Error(w, "rewind not available", http.StatusServiceUnavailable)
		return
	}

	if err := s.rewind.Restore(hash); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Working tree just changed; nudge the UI even though fsnotify will fire too.
	s.sendMessage(DiffsChangedEvent{})

	w.WriteHeader(http.StatusNoContent)
}
