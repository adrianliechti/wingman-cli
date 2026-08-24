//go:build windows

package terminal

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const ptySupported = true

// _PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE is the STARTUPINFOEX attribute that binds
// a child process to a pseudo console. It is absent from x/sys/windows.
const procThreadAttributePseudoConsole = 0x00020016

// windowsPTY runs a command under a Windows ConPTY (pseudo console). Windows
// cannot start a process attached to a pseudo console through os/exec, because
// the console handle has to be passed as a process-thread attribute at creation
// time (see golang.org/issue/62708). It is done here directly with CreateProcess.
//
// I/O flows through two anonymous pipes: what we write reaches the child's stdin;
// what the child writes to stdout/stderr we read back. ConPTY has no notion of a
// foreground process group, so HasForegroundProcess is always false.
type windowsPTY struct {
	in  *os.File
	out *os.File

	process *os.Process

	mu       sync.Mutex
	console  windows.Handle
	waitErr  error
	waited   chan struct{}
	closedCn bool
}

func startPTY(spec CommandSpec, cols, rows int) (Terminal, error) {
	// Child stdin: consoleIn read end (console-owned), inWrite write end (ours).
	consoleIn, inWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	// Child stdout: outRead read end (ours), consoleOut write end (console-owned).
	outRead, consoleOut, err := os.Pipe()
	if err != nil {
		_ = consoleIn.Close()
		_ = inWrite.Close()
		return nil, err
	}

	var console windows.Handle
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	err = windows.CreatePseudoConsole(size, windows.Handle(consoleIn.Fd()), windows.Handle(consoleOut.Fd()), 0, &console)

	// The console duplicated the handles it needs; our copies of the ends handed
	// to it are no longer required regardless of success.
	_ = consoleIn.Close()
	_ = consoleOut.Close()

	if err != nil {
		_ = inWrite.Close()
		_ = outRead.Close()
		return nil, fmt.Errorf("create pseudo console: %w", err)
	}

	p := &windowsPTY{
		in:      inWrite,
		out:     outRead,
		console: console,
		waited:  make(chan struct{}),
	}

	process, err := p.launch(spec)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	p.process = process

	// When the child exits, close the pseudo console so its write end of the
	// output pipe is released and our reader reaches EOF. Without this the read
	// loop would block forever after the command finished.
	go func() {
		state, err := process.Wait()
		p.mu.Lock()
		if err != nil {
			p.waitErr = err
		} else if state != nil && !state.Success() {
			p.waitErr = &exitError{code: state.ExitCode()}
		}
		p.mu.Unlock()
		close(p.waited)
		p.closeConsole()
	}()

	return p, nil
}

// exitError reports a non-zero process exit without importing os/exec.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("process exited with code %d", e.code) }

// launch starts the command bound to the pseudo console through CreateProcess
// with a STARTUPINFOEX carrying the pseudo-console attribute.
func (p *windowsPTY) launch(spec CommandSpec) (*os.Process, error) {
	argv := append([]string{spec.Path}, spec.Args...)
	commandLine := windows.ComposeCommandLine(argv)

	commandLinePtr, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		return nil, err
	}

	var dirPtr *uint16
	if spec.Dir != "" {
		if dirPtr, err = windows.UTF16PtrFromString(spec.Dir); err != nil {
			return nil, err
		}
	}

	envBlock, err := environmentBlock(commandEnv(spec.Env))
	if err != nil {
		return nil, err
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrList.Delete()

	if err := attrList.Update(
		procThreadAttributePseudoConsole,
		unsafe.Pointer(p.console),
		unsafe.Sizeof(p.console),
	); err != nil {
		return nil, fmt.Errorf("attach pseudo console: %w", err)
	}

	startupInfo := windows.StartupInfoEx{}
	startupInfo.StartupInfo.Cb = uint32(unsafe.Sizeof(startupInfo))
	startupInfo.ProcThreadAttributeList = attrList.List()

	// Without STARTF_USESTDHANDLES the child inherits the parent's console
	// handles and writes there instead of into the pseudo console. The std
	// handle fields stay zero: the pseudo-console attribute supplies them.
	startupInfo.StartupInfo.Flags = windows.STARTF_USESTDHANDLES

	var procInfo windows.ProcessInformation

	// EXTENDED_STARTUPINFO_PRESENT is required for the attribute list to apply;
	// CREATE_UNICODE_ENVIRONMENT matches the UTF-16 environment block.
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)

	if err := windows.CreateProcess(
		nil,
		commandLinePtr,
		nil,
		nil,
		false,
		flags,
		envBlock,
		dirPtr,
		&startupInfo.StartupInfo,
		&procInfo,
	); err != nil {
		return nil, fmt.Errorf("start %q: %w", spec.Path, err)
	}

	// The primary thread handle is not needed; os.FindProcess adopts the process
	// for waiting and signalling, so the raw process handle is released too.
	_ = windows.CloseHandle(procInfo.Thread)
	defer windows.CloseHandle(procInfo.Process)

	process, err := os.FindProcess(int(procInfo.ProcessId))
	if err != nil {
		return nil, err
	}

	return process, nil
}

func (p *windowsPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *windowsPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *windowsPTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return ErrUnsupported
	}
	if err := windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)}); err != nil {
		return fmt.Errorf("resize pseudo console: %w", err)
	}
	return nil
}

func (p *windowsPTY) ProcessID() int {
	if p.process == nil {
		return 0
	}
	return p.process.Pid
}

// HasForegroundProcess has no ConPTY equivalent, so the interactive shell is
// always treated as idle. Callers use this only to warn before closing a busy
// terminal; on Windows that guard is simply skipped.
func (p *windowsPTY) HasForegroundProcess() bool {
	return false
}

// Wait blocks until the child exits. The exit result is captured by the
// background waiter started in startPTY, so this only observes it.
func (p *windowsPTY) Wait() error {
	if p.process == nil {
		return nil
	}
	<-p.waited
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

func (p *windowsPTY) Close() error {
	if p.process != nil {
		_ = p.process.Kill()
	}

	p.closeConsole()

	var err error
	if p.in != nil {
		err = p.in.Close()
	}
	if p.out != nil {
		if cerr := p.out.Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// closeConsole releases the pseudo console once. Closing it signals the child
// that its console went away and releases the console-side pipe ends, which lets
// the output reader reach EOF.
func (p *windowsPTY) closeConsole() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closedCn || p.console == 0 {
		return
	}
	p.closedCn = true
	windows.ClosePseudoConsole(p.console)
	p.console = 0
}

// environmentBlock builds the null-separated, double-null-terminated UTF-16
// environment block CreateProcess expects. A nil slice yields a nil block so the
// child inherits the parent environment.
func environmentBlock(env []string) (*uint16, error) {
	if env == nil {
		return nil, nil
	}

	var buffer []uint16
	for _, entry := range env {
		encoded, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		buffer = append(buffer, encoded...) // includes the trailing NUL
	}
	buffer = append(buffer, 0) // final terminator for the empty environment case

	return &buffer[0], nil
}
