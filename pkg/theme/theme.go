package theme

import (
	"github.com/gdamore/tcell/v2"
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
	Background tcell.Color
	Foreground tcell.Color
	Selection  tcell.Color
	Cursor     tcell.Color
	Black      tcell.Color
	Red        tcell.Color
	Green      tcell.Color
	Yellow     tcell.Color
	Blue       tcell.Color
	Magenta    tcell.Color
	Cyan       tcell.Color
	White      tcell.Color
	BrBlack    tcell.Color
	BrRed      tcell.Color
	BrGreen    tcell.Color
	BrYellow   tcell.Color
	BrBlue     tcell.Color
	BrMagenta  tcell.Color
	BrCyan     tcell.Color
	BrWhite    tcell.Color
}

func SetDark() {
	Default = Theme{
		IsLight:    false,
		Background: tcell.GetColor("#161821"),
		Foreground: tcell.GetColor("#c6c8d1"),
		Selection:  tcell.GetColor("#272c42"),
		Cursor:     tcell.GetColor("#c6c8d1"),
		Black:      tcell.GetColor("#1e2132"),
		Red:        tcell.GetColor("#e27878"),
		Green:      tcell.GetColor("#b4be82"),
		Yellow:     tcell.GetColor("#e2a478"),
		Blue:       tcell.GetColor("#84a0c6"),
		Magenta:    tcell.GetColor("#a093c7"),
		Cyan:       tcell.GetColor("#89b8c2"),
		White:      tcell.GetColor("#c6c8d1"),
		BrBlack:    tcell.GetColor("#6b7089"),
		BrRed:      tcell.GetColor("#e98989"),
		BrGreen:    tcell.GetColor("#c0ca8e"),
		BrYellow:   tcell.GetColor("#e9b189"),
		BrBlue:     tcell.GetColor("#91acd1"),
		BrMagenta:  tcell.GetColor("#ada0d3"),
		BrCyan:     tcell.GetColor("#95c4ce"),
		BrWhite:    tcell.GetColor("#d2d4de"),
	}
}

func SetLight() {
	Default = Theme{
		IsLight:    true,
		Background: tcell.GetColor("#e8e9ec"),
		Foreground: tcell.GetColor("#33374c"),
		Selection:  tcell.GetColor("#cacdd7"),
		Cursor:     tcell.GetColor("#33374c"),
		Black:      tcell.GetColor("#dcdfe7"),
		Red:        tcell.GetColor("#cc517a"),
		Green:      tcell.GetColor("#668e3d"),
		Yellow:     tcell.GetColor("#c57339"),
		Blue:       tcell.GetColor("#2d539e"),
		Magenta:    tcell.GetColor("#7759b4"),
		Cyan:       tcell.GetColor("#3f83a6"),
		White:      tcell.GetColor("#33374c"),
		BrBlack:    tcell.GetColor("#8389a3"),
		BrRed:      tcell.GetColor("#cc3768"),
		BrGreen:    tcell.GetColor("#598030"),
		BrYellow:   tcell.GetColor("#b6662d"),
		BrBlue:     tcell.GetColor("#22478e"),
		BrMagenta:  tcell.GetColor("#6845ad"),
		BrCyan:     tcell.GetColor("#327698"),
		BrWhite:    tcell.GetColor("#262a3f"),
	}
}
