package inline

type Key int

const (
	KeyRune Key = iota
	KeyEnter
	KeyTab
	KeyBacktab
	KeyEsc
	KeyBackspace
	KeyDelete
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyCtrl
)

type Event any

type KeyEvent struct {
	Key  Key
	Rune rune
	Alt  bool
}

type PasteEvent struct {
	Text string
}

// FocusEvent reports whether the terminal window gained or lost focus.
type FocusEvent struct {
	Focused bool
}

type MouseKind int

const (
	MouseWheel MouseKind = iota
	MousePress
	MouseDrag
	MouseRelease
)

type MouseEvent struct {
	Kind       MouseKind
	WheelDelta int
	X          int
	Y          int
}

type ResizeEvent struct {
	Width  int
	Height int
}
