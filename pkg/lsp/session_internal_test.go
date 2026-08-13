package lsp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLocationResponse(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []Location
		wantErr bool
	}{
		{
			name:    "null",
			payload: `null`,
		},
		{
			name:    "empty array",
			payload: `[]`,
		},
		{
			name:    "single location",
			payload: `{"uri":"file:///a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}`,
			want:    []Location{{URI: "file:///a.go", Range: Range{Start: Position{Line: 1, Character: 2}, End: Position{Line: 1, Character: 5}}}},
		},
		{
			name:    "location array",
			payload: `[{"uri":"file:///a.go","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":4}}}]`,
			want:    []Location{{URI: "file:///a.go", Range: Range{Start: Position{Line: 3}, End: Position{Line: 3, Character: 4}}}},
		},
		{
			name:    "location links use the selection range",
			payload: `[{"targetUri":"file:///b.go","targetRange":{"start":{"line":1,"character":0},"end":{"line":9,"character":0}},"targetSelectionRange":{"start":{"line":2,"character":5},"end":{"line":2,"character":9}}}]`,
			want:    []Location{{URI: "file:///b.go", Range: Range{Start: Position{Line: 2, Character: 5}, End: Position{Line: 2, Character: 9}}}},
		},
		{
			name:    "unexpected shape",
			payload: `"nonsense"`,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseLocationResponse(json.RawMessage(test.payload))
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseLocationResponse(%s) = %+v, want an error", test.payload, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLocationResponse(%s): %v", test.payload, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("parseLocationResponse(%s) = %+v, want %+v", test.payload, got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Fatalf("location %d = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestParseCompletionResponse(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []string
	}{
		{name: "null", payload: `null`},
		{name: "array", payload: `[{"label":"Alpha"}]`, want: []string{"Alpha"}},
		{name: "list", payload: `{"isIncomplete":true,"items":[{"label":"Beta"}]}`, want: []string{"Beta"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items, err := parseCompletionResponse(json.RawMessage(test.payload))
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != len(test.want) {
				t.Fatalf("items = %+v, want %v", items, test.want)
			}
			for i, want := range test.want {
				if items[i].Label != want {
					t.Fatalf("item %d = %q, want %q", i, items[i].Label, want)
				}
			}
		})
	}
}

func TestGetSessionStopsRestartingAfterRepeatedCrashes(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root)
	project := projectRoot{
		Dir:    root,
		Server: Server{Name: "test-server", Command: filepath.Join(root, "missing-language-server")},
	}
	key := projectKey(project)

	// Each round simulates a session that started and then died.
	for attempt := 1; attempt <= maxRestarts; attempt++ {
		manager.sessions[key] = &Session{}
		if _, err := manager.getSession(context.Background(), project); err == nil {
			t.Fatalf("attempt %d: expected the restart to fail", attempt)
		}
		if manager.restarts[key] != attempt {
			t.Fatalf("attempt %d: restarts = %d, want %d", attempt, manager.restarts[key], attempt)
		}
	}

	manager.sessions[key] = &Session{}
	_, err := manager.getSession(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to apply", err)
	}

	// The cap must survive the dead session being dropped from the map.
	if _, err := manager.getSession(context.Background(), project); err == nil || !strings.Contains(err.Error(), "not restarting") {
		t.Fatalf("err = %v, want the restart cap to stay in effect", err)
	}
}
