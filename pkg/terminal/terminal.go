package terminal

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const (
	maxScrollback = 256 << 10
	compactSlack  = 64 << 10

	readChunk        = 32 << 10
	subscriberBuffer = 64

	DefaultCols = 80
	DefaultRows = 24
)

var ErrUnsupported = errors.New("terminal sessions are not supported on this platform")

type Session struct {
	id    string
	title string
	shell string

	cmd *exec.Cmd
	pty *os.File

	done   chan struct{}
	onExit func(*Session)

	mu     sync.Mutex
	buf    []byte
	subs   map[chan []byte]struct{}
	cols   int
	rows   int
	closed bool
}

func Supported() bool {
	return ptySupported
}

func newSession(id, shell, dir string, cols, rows int, onExit func(*Session)) (*Session, error) {
	if !ptySupported {
		return nil, ErrUnsupported
	}

	cols, rows = normalizeSize(cols, rows)

	cmd := exec.Command(shell)
	cmd.Dir = dir
	cmd.Env = terminalEnv()

	f, err := startPTY(cmd, cols, rows)
	if err != nil {
		return nil, err
	}

	s := &Session{
		id:    id,
		title: shellName(shell),
		shell: shell,

		cmd: cmd,
		pty: f,

		done:   make(chan struct{}),
		onExit: onExit,
		subs:   map[chan []byte]struct{}{},

		cols: cols,
		rows: rows,
	}

	go s.read()

	return s, nil
}

func (s *Session) ID() string {
	return s.id
}

func (s *Session) Title() string {
	return s.title
}

func (s *Session) Shell() string {
	return s.shell
}

func (s *Session) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) Exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// HasForegroundProcess reports whether the PTY foreground process group is a
// command rather than the interactive shell itself.
func (s *Session) HasForegroundProcess() bool {
	if s.Exited() {
		return false
	}
	return hasForegroundProcess(s.pty, s.cmd)
}

func (s *Session) Write(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_, err := s.pty.Write(p)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	cols, rows = normalizeSize(cols, rows)

	s.mu.Lock()
	if s.closed || (cols == s.cols && rows == s.rows) {
		s.mu.Unlock()
		return nil
	}
	s.cols, s.rows = cols, rows
	s.mu.Unlock()

	return setPTYSize(s.pty, cols, rows)
}

// Subscribe returns the scrollback captured so far plus a channel of
// subsequent output. The channel is closed when the session ends or when the
// subscriber falls too far behind; cancel must always be called.
func (s *Session) Subscribe() (snapshot []byte, ch <-chan []byte, cancel func()) {
	c := make(chan []byte, subscriberBuffer)

	s.mu.Lock()
	snapshot = append([]byte(nil), s.buf...)
	if s.closed {
		s.mu.Unlock()
		close(c)
		return snapshot, c, func() {}
	}
	s.subs[c] = struct{}{}
	s.mu.Unlock()

	return snapshot, c, func() { s.unsubscribe(c) }
}

func (s *Session) unsubscribe(c chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[c]; !ok {
		return
	}
	delete(s.subs, c)
	close(c)
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return nil
	}
	s.mu.Unlock()

	killProcess(s.cmd)
	err := s.pty.Close()
	<-s.done
	return err
}

func (s *Session) read() {
	buf := make([]byte, readChunk)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.publish(buf[:n])
		}
		if err != nil {
			break
		}
	}
	s.finish()
}

func (s *Session) publish(p []byte) {
	chunk := append([]byte(nil), p...)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.buf = append(s.buf, chunk...)
	if len(s.buf) > maxScrollback+compactSlack {
		s.buf = append(make([]byte, 0, maxScrollback+compactSlack), s.buf[len(s.buf)-maxScrollback:]...)
	}

	for c := range s.subs {
		select {
		case c <- chunk:
		default:
			delete(s.subs, c)
			close(c)
		}
	}
}

func (s *Session) finish() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	subs := s.subs
	s.subs = map[chan []byte]struct{}{}
	s.mu.Unlock()

	for c := range subs {
		close(c)
	}

	_ = s.pty.Close()
	_ = s.cmd.Wait()

	close(s.done)

	if s.onExit != nil {
		s.onExit(s)
	}
}

func normalizeSize(cols, rows int) (int, int) {
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}
	return min(cols, 1000), min(rows, 1000)
}

func terminalEnv() []string {
	drop := []string{"TERM=", "COLORTERM=", "TERM_PROGRAM=", "WINGMAN="}
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		skip := false
		for _, p := range drop {
			if strings.HasPrefix(kv, p) {
				skip = true
				break
			}
		}
		if !skip {
			env = append(env, kv)
		}
	}
	return append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"TERM_PROGRAM=wingman",
		"WINGMAN=1",
	)
}
