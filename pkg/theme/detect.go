package theme

import (
	"os"
	"strconv"
	"strings"
)

func isLightBackground() bool {
	if val := os.Getenv("COLORFGBG"); val != "" {
		parts := strings.Split(val, ";")

		if len(parts) >= 2 {
			bg, _ := strconv.Atoi(parts[len(parts)-1])

			return bg == 7 || bg == 15
		}
	}

	return queryTerminalBackground()
}