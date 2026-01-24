//go:build windows

package theme

// queryTerminalBackground returns false on Windows as the OSC 11 query
// is not reliably supported across Windows terminals.
func queryTerminalBackground() bool {
	return false
}