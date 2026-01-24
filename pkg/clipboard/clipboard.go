package clipboard

// Content represents a piece of clipboard content (text or image)
type Content struct {
	Text  string
	Image *string // base64 data URL, e.g. "data:image/png;base64,..."
}