package inline

import (
	"strconv"
	"strings"
)

// pasteKey accepts CSI-u/Kitty and xterm modifyOtherKeys encodings without
// changing the terminal's keyboard mode. Native bracketed paste stays separate.
// Kitty encoding: https://sw.kovidgoyal.net/kitty/keyboard-protocol/
func pasteKey(seq string) (KeyEvent, bool) {
	if seq == "2;2~" { // Shift+Insert, if the terminal forwards it.
		return KeyEvent{Key: KeyCtrl, Rune: 'v'}, true
	}
	if len(seq) == 0 {
		return KeyEvent{}, false
	}
	parts := strings.Split(seq[:len(seq)-1], ";")
	var code, modifiers string
	switch seq[len(seq)-1] {
	case 'u':
		// Associated text belongs to the terminal's text-input path, not paste.
		if len(parts) != 2 {
			return KeyEvent{}, false
		}
		keys := strings.Split(parts[0], ":")
		if len(keys) > 3 {
			return KeyEvent{}, false
		}
		code = keys[0]
		if len(keys) == 3 && keys[2] != "" {
			code = keys[2] // Physical base key on a non-Latin keyboard layout.
		}
		modifiers = parts[1]
	case '~':
		if len(parts) != 3 || parts[0] != "27" {
			return KeyEvent{}, false
		}
		code, modifiers = parts[2], parts[1]
	default:
		return KeyEvent{}, false
	}
	if code != "118" && code != "86" { // v/V
		return KeyEvent{}, false
	}
	modifier, eventType, hasEventType := strings.Cut(modifiers, ":")
	if hasEventType && eventType != "1" {
		// A held key or its release must not read and insert the clipboard again.
		return KeyEvent{}, false
	}
	value, err := strconv.Atoi(modifier)
	if err != nil || value < 1 || value > 256 {
		return KeyEvent{}, false
	}
	value--                     // Encoded modifiers are 1 + the modifier bit mask.
	chord := value & 63         // Ignore Caps Lock and Num Lock.
	if chord < 4 || chord > 7 { // Ctrl, optionally Shift and/or Alt.
		return KeyEvent{}, false
	}
	return KeyEvent{Key: KeyCtrl, Rune: 'v', Alt: chord&2 != 0}, true
}
