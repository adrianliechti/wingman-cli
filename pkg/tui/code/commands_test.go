package code

import (
	"slices"
	"testing"
)

func TestSlashToken(t *testing.T) {
	tests := []struct {
		text      string
		cursor    int
		wantStart int
		wantToken string
		wantOK    bool
	}{
		{"/mod", 4, 0, "/mod", true},
		{"/", 1, 0, "/", true},
		{"fix this then /sim", 18, 14, "/sim", true},
		{"line one\n/mod", 13, 9, "/mod", true},
		{"tab\t/mod", 8, 4, "/mod", true},
		{"", 0, 0, "", false},
		{"hello", 5, 0, "", false},
		{"/mod ", 5, 0, "", false},
		{"see /Users/adrian", 17, 0, "", false},
		{"https://example.com", 19, 0, "", false},
		{"path/to", 7, 0, "", false},
		{"fix this then /sim", 10, 0, "", false},
	}

	for _, tt := range tests {
		start, token, ok := slashToken([]rune(tt.text), tt.cursor)
		if ok != tt.wantOK || token != tt.wantToken || (ok && start != tt.wantStart) {
			t.Errorf("slashToken(%q, %d) = (%d, %q, %v), want (%d, %q, %v)",
				tt.text, tt.cursor, start, token, ok, tt.wantStart, tt.wantToken, tt.wantOK)
		}
	}
}

func TestEditorReplaceRange(t *testing.T) {
	e := NewEditor()
	e.SetText("do /sim now")
	e.cursor = 7

	e.ReplaceRange(3, 7, "/simplify ")

	if got := e.Text(); got != "do /simplify  now" {
		t.Fatalf("Text() = %q", got)
	}
	if e.cursor != 13 {
		t.Fatalf("cursor = %d, want 13", e.cursor)
	}
}

func TestApplyContextSelectionKeepsUnofferedAttachments(t *testing.T) {
	a := &App{}
	a.pendingFiles = []string{"/abs/outside.pdf", "src/main.go", ".env"}

	files := []fileMatch{{Path: "src/main.go"}, {Path: "docs/readme.md"}}
	ids := []string{
		contextFileID("docs/readme.md"),
		contextFileID("/abs/outside.pdf"),
		contextFileID(".env"),
	}

	a.applyContextSelection(nil, files, ids)

	want := []string{"docs/readme.md", "/abs/outside.pdf", ".env"}
	if !slices.Equal(a.pendingFiles, want) {
		t.Fatalf("pendingFiles = %v, want %v", a.pendingFiles, want)
	}
}
