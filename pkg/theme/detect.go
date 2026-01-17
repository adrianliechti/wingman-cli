package theme

import (
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func isLightBackground() bool {
	if val := os.Getenv("COLORFGBG"); val != "" {
		parts := strings.Split(val, ";")
		if len(parts) >= 2 {
			bg, _ := strconv.Atoi(parts[len(parts)-1])
			return bg == 7 || bg == 15
		}
	}

	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		return false
	}

	oldState, err := term.MakeRaw(fd)

	if err != nil {
		return false
	}

	defer term.Restore(fd, oldState)

	os.Stdout.WriteString("\x1b]11;?\x07")

	buf := make([]byte, 64)
	done := make(chan int, 1)

	go func() {
		n, _ := os.Stdin.Read(buf)
		done <- n
	}()

	select {
	case n := <-done:
		return parseLuma(string(buf[:n])) > 0.5
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

func parseLuma(s string) float64 {
	i := strings.Index(s, "rgb:")

	if i == -1 {
		return 0
	}

	s = s[i+4:]
	parts := strings.SplitN(s, "/", 3)

	if len(parts) < 3 {
		return 0
	}

	r := parseHex(parts[0])
	g := parseHex(parts[1])
	b := parseHex(strings.TrimRight(parts[2], "\x07\x1b\\"))

	return 0.299*float64(r)/255 + 0.587*float64(g)/255 + 0.114*float64(b)/255
}

func parseHex(s string) int {
	if len(s) == 4 {
		s = s[:2]
	}

	v, _ := strconv.ParseInt(s, 16, 32)

	return int(v)
}
