//go:build !windows

package theme

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

func queryTerminalBackground() bool {
	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		return false
	}

	oldState, err := term.MakeRaw(fd)

	if err != nil {
		return false
	}

	os.Stdout.WriteString("\x1b]11;?\x07")

	buf := make([]byte, 64)
	done := make(chan int, 1)

	go func() {
		n, _ := os.Stdin.Read(buf)
		done <- n
	}()

	var result bool

	select {
	case n := <-done:
		result = parseLuma(string(buf[:n])) > 0.5
	case <-time.After(100 * time.Millisecond):
		result = false
	}

	// Drain any pending input that might arrive after timeout by doing
	// non-blocking reads while still in raw mode
	syscall.SetNonblock(fd, true)
	drainBuf := make([]byte, 64)

	for {
		n, _ := syscall.Read(fd, drainBuf)

		if n <= 0 {
			break
		}
	}
	syscall.SetNonblock(fd, false)

	term.Restore(fd, oldState)

	return result
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