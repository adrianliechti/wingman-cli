package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/theme"
)

func TestEditorChromeStaysWithinBounds(t *testing.T) {
	theme.SetDark()
	editor := NewEditor()
	editor.SetText("one two three four five six seven eight nine ten")
	lines, cursor := editor.Render(40, 6, EditorChrome{
		TopLabel:    colored(theme.Default.Yellow, "PLAN"),
		BottomRight: dim("GPT 5.6 Sol · medium"),
		TopColor:    theme.Default.Yellow,
		Attachments: []string{" image 1  file.go"},
	})
	if len(lines) > 6 {
		t.Fatalf("editor rendered %d rows, max 6", len(lines))
	}
	for i, line := range lines {
		if ansi.Width(line) > 40 {
			t.Fatalf("row %d width = %d: %q", i, ansi.Width(line), line)
		}
	}
	if cursor.Row < 0 || cursor.Row >= len(lines) || cursor.Col < 0 || cursor.Col >= 40 {
		t.Fatalf("cursor outside editor: %+v in %d rows", cursor, len(lines))
	}
	if lines[len(lines)-3] != "" {
		t.Fatalf("attachments are not separated from input: %q", lines)
	}
	if strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "@ manage") {
		t.Fatalf("composer still shows the manage hint: %q", lines)
	}
}
