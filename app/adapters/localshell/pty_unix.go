//go:build !windows

package localshell

import (
	"os"
	"os/exec"
	"sync"
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
	// mu guards the MASTER'S LIFETIME, not its I/O. Read and Write go
	// straight to the file, which is safe concurrently and must not be
	// serialised — a read blocks until the shell says something, and holding
	// a lock across that would stall every write behind it.
	//
	// What is not safe is the pair this lock does cover: an ioctl reads the
	// descriptor out of the file, and Close invalidates it. The pump closes
	// the master the moment the shell exits, and a resize can arrive from the
	// interface at that instant — a window dragged as a session ends is
	// exactly the case. The race detector found it as a test reading the size
	// while the pump closed underneath, but the production pair is
	// ResizeLocalShell against a shell that has just exited.
	mu     sync.Mutex
	closed bool
	ptmx   *os.File
	cmd    *exec.Cmd
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
	return &ptyProcess{ptmx: pollable(ptmx), cmd: cmd}, nil
}

// pollable returns the master as a file the runtime can interrupt, falling
// back to the original if it cannot.
//
// WITHOUT THIS, CLOSING THE MASTER DOES NOT WAKE A READ ALREADY PARKED IN THE
// KERNEL, and the pump goroutine blocks for the life of the process. The
// library hands back a file opened in blocking mode, which the runtime does
// not register with its poller, so a read goes straight to the syscall and
// stays there — Close marks the file closed without disturbing it.
//
// That only becomes visible when something still holds the slave open. A
// shell's child that puts ITSELF in a new process group survives the group
// signal, is reparented away, and keeps the terminal open: on Linux `sh`
// running `sleep` does exactly that, which is how this was found. The shell
// dies, nothing reaps it, the master never reaches end of file, and stopping
// the session never returns.
//
// Duplicating the descriptor and re-wrapping it non-blocking puts it under
// the poller, so Close interrupts the read with a plain "file already closed"
// and the pump leaves. A failure here is not worth refusing a shell over: the
// caller gets the blocking file it would have had anyway.
func pollable(f *os.File) *os.File {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return f
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return f
	}
	dup := os.NewFile(uintptr(fd), f.Name())
	// The original wrapper goes, not the terminal: the duplicate holds it
	// open, and leaving both would mean two files racing to close one
	// descriptor.
	_ = f.Close()
	return dup
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.ptmx.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.ptmx.Write(b) }

// Resize sets the window size, which delivers SIGWINCH to the foreground
// process group on the other side.
func (p *ptyProcess) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		// The shell has gone. Nothing to resize, and it is not an error worth
		// showing somebody: the pane is about to close anyway.
		return nil
	}
	return pty.Setsize(p.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

// size reports the terminal's current window size.
func (p *ptyProcess) size() (cols, rows uint16, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, 0, os.ErrClosed
	}

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
	//
	// THROUGH Close, NOT DIRECTLY. There must be exactly one path that closes
	// the master, or the lifetime lock guards nothing: closing here would let
	// a resize pass the closed check and then read the descriptor while this
	// call destroys it. Closing a pane mid-window-drag is that sequence.
	p.Close()
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
//
// Idempotent, and it marks the master closed under the same lock the size and
// resize paths take, so neither can be holding the descriptor while it goes.
func (p *ptyProcess) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	_ = p.ptmx.Close()
}
