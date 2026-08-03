package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"

	"github.com/adrianliechti/wingman-agent/pkg/terminal"
)

type TerminalEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
}

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func terminalEntry(s *terminal.Session) TerminalEntry {
	cols, rows := s.Size()
	return TerminalEntry{
		ID:    s.ID(),
		Title: s.Title(),
		Cols:  cols,
		Rows:  rows,
	}
}

func (s *Server) handleTerminals(w http.ResponseWriter, _ *http.Request) {
	sessions := s.terminals.List()
	out := make([]TerminalEntry, 0, len(sessions))
	for _, t := range sessions {
		out = append(out, terminalEntry(t))
	}
	writeJSON(w, out)
}

func (s *Server) handleNewTerminal(w http.ResponseWriter, r *http.Request) {
	if !terminal.Supported() {
		http.Error(w, "terminals are not supported on this platform", http.StatusNotImplemented)
		return
	}

	var body struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	t, err := s.terminals.Create(body.Cols, body.Rows)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.broadcast(Frame{Type: EvtTerminalsChanged})
	writeJSON(w, terminalEntry(t))
}

func (s *Server) handleDeleteTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "terminal id required", http.StatusBadRequest)
		return
	}
	if !s.terminals.Remove(id) {
		http.Error(w, "terminal not found", http.StatusNotFound)
		return
	}
	s.broadcast(Frame{Type: EvtTerminalsChanged})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	t := s.terminals.Get(r.PathValue("id"))
	if t == nil {
		http.Error(w, "terminal not found", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	conn.SetReadLimit(1 << 20)

	snapshot, out, cancel := t.Subscribe()
	defer cancel()

	writerDone := make(chan struct{})
	go func() {
		// Closing the connection here also unblocks the read loop below, so a
		// dropped subscription (exit or slow consumer) tears down both halves.
		defer close(writerDone)
		defer conn.CloseNow()

		if len(snapshot) > 0 && !writeTerminalFrame(conn, websocket.MessageBinary, snapshot) {
			return
		}
		for chunk := range out {
			if !writeTerminalFrame(conn, websocket.MessageBinary, chunk) {
				return
			}
		}
		if t.Exited() {
			data, _ := json.Marshal(terminalMessage{Type: "exit"})
			writeTerminalFrame(conn, websocket.MessageText, data)
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	}()

	defer func() {
		cancel()
		<-writerDone
	}()

	for {
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		if typ == websocket.MessageBinary {
			if err := t.Write(data); err != nil {
				return
			}
			continue
		}

		var msg terminalMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "input":
			if err := t.Write([]byte(msg.Data)); err != nil {
				return
			}
		case "resize":
			_ = t.Resize(msg.Cols, msg.Rows)
		}
	}
}

func writeTerminalFrame(conn *websocket.Conn, typ websocket.MessageType, data []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), wsWriteTimeout)
	defer cancel()
	return conn.Write(ctx, typ, data) == nil
}

func (s *Server) onTerminalExit(string) {
	s.broadcast(Frame{Type: EvtTerminalsChanged})
}
