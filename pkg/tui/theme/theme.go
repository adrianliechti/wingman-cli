package theme

import (
	"fmt"

	"github.com/adrianliechti/wingman-agent/pkg/tui/ansi"
)

var Default Theme

func Auto() {
	SetDark()

	if isLightBackground() {
		SetLight()
	}
}

type Theme struct {
	IsLight    bool
	Background ansi.Color
	Foreground ansi.Color
	Selection  ansi.Color
	Cursor     ansi.Color
	Black      ansi.Color
	Red        ansi.Color
	Green      ansi.Color
	Yellow     ansi.Color
	Blue       ansi.Color
	Magenta    ansi.Color
	Cyan       ansi.Color
	White      ansi.Color
	BrBlack    ansi.Color
	BrRed      ansi.Color
	BrGreen    ansi.Color
	BrYellow   ansi.Color
	BrBlue     ansi.Color
	BrMagenta  ansi.Color
	BrCyan     ansi.Color
	BrWhite    ansi.Color
}

// Signature identifies every palette value so render caches can detect when
// the terminal-aware palette has been reinitialized.
func (t Theme) Signature() string {
	return fmt.Sprintf("%t:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x:%06x",
		t.IsLight, t.Background.Hex(), t.Foreground.Hex(), t.Selection.Hex(), t.Cursor.Hex(),
		t.Black.Hex(), t.Red.Hex(), t.Green.Hex(), t.Yellow.Hex(), t.Blue.Hex(),
		t.Magenta.Hex(), t.Cyan.Hex(), t.White.Hex(), t.BrBlack.Hex(), t.BrRed.Hex(),
		t.BrGreen.Hex(), t.BrYellow.Hex(), t.BrBlue.Hex(), t.BrMagenta.Hex(), t.BrCyan.Hex(), t.BrWhite.Hex())
}

func SetDark() {
	Default = Theme{
		IsLight:    false,
		Background: ansi.Hex("#161821"),
		Foreground: ansi.Hex("#c6c8d1"),
		Selection:  ansi.Hex("#272c42"),
		Cursor:     ansi.Hex("#c6c8d1"),
		Black:      ansi.Hex("#1e2132"),
		Red:        ansi.Hex("#e27878"),
		Green:      ansi.Hex("#b4be82"),
		Yellow:     ansi.Hex("#e2a478"),
		Blue:       ansi.Hex("#84a0c6"),
		Magenta:    ansi.Hex("#a093c7"),
		Cyan:       ansi.Hex("#89b8c2"),
		White:      ansi.Hex("#c6c8d1"),
		BrBlack:    ansi.Hex("#6b7089"),
		BrRed:      ansi.Hex("#e98989"),
		BrGreen:    ansi.Hex("#c0ca8e"),
		BrYellow:   ansi.Hex("#e9b189"),
		BrBlue:     ansi.Hex("#91acd1"),
		BrMagenta:  ansi.Hex("#ada0d3"),
		BrCyan:     ansi.Hex("#95c4ce"),
		BrWhite:    ansi.Hex("#d2d4de"),
	}
}

func SetLight() {
	Default = Theme{
		IsLight:    true,
		Background: ansi.Hex("#e8e9ec"),
		Foreground: ansi.Hex("#33374c"),
		Selection:  ansi.Hex("#c9cdd7"),
		Cursor:     ansi.Hex("#33374c"),
		Black:      ansi.Hex("#dcdfe7"),
		Red:        ansi.Hex("#cc517a"),
		Green:      ansi.Hex("#668e3d"),
		Yellow:     ansi.Hex("#c57339"),
		Blue:       ansi.Hex("#2d539e"),
		Magenta:    ansi.Hex("#7759b4"),
		Cyan:       ansi.Hex("#3f83a6"),
		White:      ansi.Hex("#33374c"),
		BrBlack:    ansi.Hex("#8389a3"),
		BrRed:      ansi.Hex("#cc3768"),
		BrGreen:    ansi.Hex("#598030"),
		BrYellow:   ansi.Hex("#b6662d"),
		BrBlue:     ansi.Hex("#22478e"),
		BrMagenta:  ansi.Hex("#6845ad"),
		BrCyan:     ansi.Hex("#327698"),
		BrWhite:    ansi.Hex("#262a3f"),
	}
}
