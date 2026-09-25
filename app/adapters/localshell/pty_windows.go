//go:build windows

package localshell

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

// A LOCAL SHELL ON WINDOWS, THROUGH ConPTY.
//
// This file used to be the whole of a decision not to have one: the PTY
// dependency the Unix side uses returns "unsupported" on Windows, and wiring
// ConPTY by hand meant a second implementation of start, resize and teardown
// "tested on a platform this project does not build for release today".
//
// THAT LAST PREMISE STOPPED BEING TRUE. CI packages windows-amd64 on every
// pull request and the release publishes it, so Windows is a platform this
// project ships — and telling somebody who downloaded that build that their
// terminal is missing because of a library's Windows story is not an answer
// they can act on.
//
// ConPTY is Windows' own pseudo-console, present since Windows 10 1809. It is
// a different shape to a Unix master/slave pair: there is no file descriptor
// to ioctl, the child is attached through a startup-info attribute, and the
// command is a COMMAND LINE rather than an argv. What follows is that shape,
// kept behind the same four-method interface the Unix file implements so
// Manager needs to know none of it.
//
// WHAT IS DIFFERENT AND WHY, stated rather than smoothed over:
//
//   - There is no SIGHUP. Hangup closes the pseudo-console, which hands the
//     child an EOF on its input — the nearest thing Windows has to a hangup,
//     and what closing a console window does to whatever is running in it.
//   - Kill terminates the process itself, because a console application that
//     ignores its input never notices the EOF above.
//   - There is no process GROUP to signal. A shell's children are not reaped
//     by killing the shell here the way they are on Unix; that is Windows'
//     model, and pretending otherwise in a comment would be worse than saying
//     it.
type ptyProcess struct {
	// mu guards the pty's LIFETIME, not its I/O — the same split the Unix
	// file makes, and for the same reason: a read parks until the shell says
	// something, and holding a lock across that would stall every write
	// behind it. What it does cover is Close against Resize, since resizing a
	// console that has been freed is a use-after-free in kernel32 rather than
	// an error return.
	mu     sync.Mutex
	closed bool
	pty    *conpty.ConPty
}

// supported reports whether this Windows can open a local shell.
//
// A RUNTIME PROBE, NOT A CONSTANT. ConPTY arrived in Windows 10 1809; the
// call is resolved from kernel32 at first use, and on anything older it is
// simply not there. Reporting support from the build tag alone would put a
// control on screen that fails when pressed — which is the failure this file
// existed to avoid, moved rather than fixed.
func supported() (bool, string) {
	if conpty.IsConPtyAvailable() {
		return true, ""
	}
	return false, UnsupportedNotice
}

// startPTY starts cmd on a new pseudo-console at the given size.
//
// Sized before the first byte, for the reason the Unix side gives: a shell
// draws its prompt as soon as it starts, and one drawn at the wrong width
// wraps in the wrong place for the rest of the session.
func startPTY(cmd *exec.Cmd, cols, rows uint16) (*ptyProcess, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	options := []conpty.ConPtyOption{conpty.ConPtyDimensions(int(cols), int(rows))}
	if cmd.Dir != "" {
		options = append(options, conpty.ConPtyWorkDir(cmd.Dir))
	}
	if cmd.Env != nil {
		options = append(options, conpty.ConPtyEnv(cmd.Env))
	}

	pty, err := conpty.Start(commandLine(cmd), options...)
	if err != nil {
		return nil, fmt.Errorf("opening a console for %s: %w", cmd.Path, err)
	}
	return &ptyProcess{pty: pty}, nil
}

// commandLine renders an exec.Cmd as the single string ConPTY takes.
//
// THE QUOTING IS NOT COSMETIC. Windows hands a process one command line and
// lets it parse its own arguments, so a path with a space in it — which on
// Windows is the ordinary case, `C:\Program Files\…` — becomes two arguments
// unless it is quoted the way the C runtime expects. syscall.EscapeArg is the
// standard library's own implementation of that rule, and using it means the
// quoting here matches what exec.Cmd itself would have produced.
func commandLine(cmd *exec.Cmd) string {
	if len(cmd.Args) == 0 {
		return syscall.EscapeArg(cmd.Path)
	}

	line := syscall.EscapeArg(cmd.Path)
	for _, arg := range cmd.Args[1:] {
		line += " " + syscall.EscapeArg(arg)
	}
	return line
}

func (p *ptyProcess) Read(b []byte) (int, error) {
	// Straight to the pty, deliberately unlocked: see mu.
	return p.pty.Read(b)
}

func (p *ptyProcess) Write(b []byte) (int, error) {
	return p.pty.Write(b)
}

// Resize changes the console's size, unless it has been closed.
func (p *ptyProcess) Resize(cols, rows uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		// The shell has exited and the console is gone. Not an error worth
		// reporting: a window dragged as a session ends is the ordinary way
		// to reach here.
		return nil
	}
	return p.pty.Resize(int(cols), int(rows))
}

// Hangup closes the pseudo-console, which is what Windows has instead of a
// hangup: the child sees its input end, exactly as it would if somebody shut
// the console window it was running in.
func (p *ptyProcess) Hangup() { p.Close() }

// Kill terminates the process on the far end.
//
// The pid, not a process group: Windows has no process group to signal, and
// the handle the library opened is not exposed — so this opens its own with
// the one right it needs and nothing else.
func (p *ptyProcess) Kill() {
	pid := p.pty.Pid()
	if pid <= 0 {
		return
	}

	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	// 1 rather than 0: an exit code of zero from a process somebody killed
	// would report a clean exit in the pane and in the log.
	_ = windows.TerminateProcess(handle, 1)
}

// Wait blocks until the process exits.
//
// A NON-ZERO EXIT IS NOT AN ERROR HERE, which matches what the Unix side ends
// up reporting: exitReason treats an *exec.ExitError as an ordinary exit and
// says nothing in the pane, because `exit 1` typed into a shell is the
// operator ending their own session rather than something PodSteer has to
// explain. Only a failure to WAIT — a handle that went away, a cancelled
// context — is worth a sentence, and that is what is returned.
func (p *ptyProcess) Wait() error {
	if _, err := p.pty.Wait(context.Background()); err != nil {
		return fmt.Errorf("waiting for the shell to exit: %w", err)
	}
	return nil
}

// Close releases the console. Idempotent: the pump closes it when the shell
// exits and Manager closes it again when the session is torn down.
func (p *ptyProcess) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true
	_ = p.pty.Close()
}
