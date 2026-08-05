package code

import (
	"strings"
	"testing"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
	"github.com/adrianliechti/wingman-agent/pkg/tui/inline"
)

func TestTwoPaneResponsiveDrillDownAndFilter(t *testing.T) {
	items := []string{"alpha.go", "beta.go"}
	o := newTwoPaneOverlay("changes", "2 files", len(items),
		func(selected bool, index int) string { return items[index] },
		func(index int) []string { return []string{"detail for " + items[index], "second line"} },
		func(index int) string { return items[index] },
	)
	o.Render(60, 12)
	if !o.narrow || o.detailView {
		t.Fatalf("initial narrow state = narrow:%v detail:%v", o.narrow, o.detailView)
	}
	o.HandleKey(inline.KeyEvent{Key: inline.KeyEnter})
	if !o.detailView {
		t.Fatal("enter did not drill into detail")
	}
	if closed := o.HandleKey(inline.KeyEvent{Key: inline.KeyEsc}); closed || o.detailView {
		t.Fatal("first escape did not return to list")
	}

	o.HandleKey(inline.KeyEvent{Key: inline.KeyRune, Rune: '/'})
	o.HandleKey(inline.KeyEvent{Key: inline.KeyRune, Rune: 'b'})
	o.HandleKey(inline.KeyEvent{Key: inline.KeyEnter})
	if len(o.filtered) != 1 || o.filtered[0] != 1 {
		t.Fatalf("filtered = %v", o.filtered)
	}
	for _, line := range o.Render(60, 12) {
		if ansi.Width(line) > 60 {
			t.Fatalf("narrow row overflow: %d", ansi.Width(line))
		}
	}

	o.Render(120, 24)
	if o.narrow {
		t.Fatal("120-column overlay remained narrow")
	}
}

func TestTwoPaneWideEnterAndRowWidth(t *testing.T) {
	items := []string{"alpha.go", "beta.go"}
	o := newTwoPaneOverlay("changes", "2 files", len(items),
		func(selected bool, index int) string { return items[index] },
		func(index int) []string {
			lines := make([]string, 30)
			for i := range lines {
				lines[i] = strings.Repeat("x", 200)
			}
			return lines
		},
		func(index int) string { return items[index] },
	)

	const width = 120
	lines := o.Render(width, 20)
	if o.narrow {
		t.Fatal("120 columns rendered narrow")
	}
	for row := 2; row < len(lines)-1; row++ {
		if got := ansi.Width(lines[row]); got != width {
			t.Fatalf("content row %d width = %d, want %d", row, got, width)
		}
	}

	o.HandleKey(inline.KeyEvent{Key: inline.KeyEnter})
	if !o.focusRight {
		t.Fatal("enter did not focus the detail pane")
	}
}
