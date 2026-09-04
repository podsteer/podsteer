//go:build !windows

package localshell

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// ptyProcess is one child on a pseudo-terminal.
//
// The master side is an ordinary file: reading it is the child's output,
// writing it is the child's input, and an ioctl on it is the window size. That
// is the whole reason this feature needs a dependency at all — allocating the
// pair and making the child's terminal its controlling terminal are ioctls the
// standard library does not expose.
type ptyProcess struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

// supported reports that this platform can open a local shell.
func supported() (bool, string) { return true, "" }

// startPTY allocates a pseudo-terminal at the given size and starts cmd on it.
//
// SIZED BEFORE THE FIRST BYTE. A shell draws its prompt as soon as it starts,
// and one drawn at 80x24 into a pane that is not 80x24 wraps in the wrong
// place for the rest of the session — a later resize redraws what comes after
// it, never what is already on screen.
func startPTY(cmd *exec.Cmd, cols, rows uint16) (*ptyProcess, error) {
	// A zero from a pane that has not been measured yet would give the child
	// a terminal with no columns, which line editing divides by.
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	// StartWithSize puts the child in a new session and makes the terminal its
	// controlling one, which is what makes job control and Ctrl+C work — and,
	// because the child becomes a session leader, its pid is also its process
	// group id, which is what Hangup and Kill below signal.
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &ptyProcess{ptmx: ptmx, cmd: cmd}, nil
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.ptmx.Write(b) }

// Resize sets the window size, which delivers SIGWINCH to the foreground
// process group on the other side.
func (p *ptyProcess) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(p.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// size reports the terminal's current window size.
func (p *ptyProcess) size() (cols, rows uint16, err error) {
	ws, err := pty.GetsizeFull(p.ptmx)
	if err != nil {
		return 0, 0, err
	}
	return ws.Cols, ws.Rows, nil
}

// Hangup asks the whole process group to leave.
//
// SIGHUP rather than SIGTERM because that is what closing a terminal means,
// and every shell already knows how to answer it: run the exit trap, save
// history, hang up the children. The group, not the leader, so a foreground
// program the shell was running goes too.
func (p *ptyProcess) Hangup() {
	p.signal(syscall.SIGHUP)
	// Closing the master hands the child's reads an EOF as well, so a shell
	// blocked on input leaves even if it ignored the signal.
	_ = p.ptmx.Close()
}

// Kill ends the process group outright, for something that ignored the hangup.
func (p *ptyProcess) Kill() { p.signal(syscall.SIGKILL) }

// signal sends to the child's process group, falling back to the child alone.
func (p *ptyProcess) signal(sig syscall.Signal) {
	if p.cmd.Process == nil {
		return
	}
	// The negative pid is the group. The child is a session leader, so its pid
	// is the group id; when the group has already gone, the single-process
	// signal is the fallback and its own failure is the answer.
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		_ = p.cmd.Process.Signal(sig)
	}
}

// Wait reaps the child.
func (p *ptyProcess) Wait() error { return p.cmd.Wait() }

// Close releases the master, tolerating the Hangup that already closed it.
func (p *ptyProcess) Close() { _ = p.ptmx.Close() }
