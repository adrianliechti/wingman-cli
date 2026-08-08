package plugin

import (
	"runtime"
	"strings"
)

const (
	rootVariable = "PLUGIN_ROOT"
	dataVariable = "PLUGIN_DATA"

	rootPlaceholder = "${" + rootVariable + "}"
	dataPlaceholder = "${" + dataVariable + "}"
)

// expand replaces every ${PLUGIN_ROOT} and ${PLUGIN_DATA} occurrence in a
// single left-to-right pass. Replacement text is never rescanned, so a root
// path that itself contains a placeholder stays literal. Anything else that
// looks like a placeholder is left alone.
func expand(value, root, data string) string {
	var sb strings.Builder

	for value != "" {
		rootIndex := strings.Index(value, rootPlaceholder)
		dataIndex := strings.Index(value, dataPlaceholder)

		index, placeholder, replacement := -1, "", ""

		switch {
		case rootIndex >= 0 && (dataIndex < 0 || rootIndex <= dataIndex):
			index, placeholder, replacement = rootIndex, rootPlaceholder, root
		case dataIndex >= 0:
			index, placeholder, replacement = dataIndex, dataPlaceholder, data
		}

		if index < 0 {
			sb.WriteString(value)
			break
		}

		sb.WriteString(value[:index])
		sb.WriteString(replacement)
		value = value[index+len(placeholder):]
	}

	return sb.String()
}

// sameVariable compares environment variable names using the platform's own
// semantics; Windows treats them case-insensitively.
func sameVariable(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}

	return a == b
}
