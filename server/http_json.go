package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid body: expected a single JSON value", http.StatusBadRequest)
		return errors.New("request body contains more than one JSON value")
	}
	return nil
}
