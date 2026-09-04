//go:build windows

package localshell

import (
	"errors"
	"os/exec"
)

// A LOCAL SHELL IS DISABLED ON WINDOWS, and this file is the whole of that
// decision.
//
// Windows has a pseudo-terminal — ConPTY — but it is a different API to the
// one every other platform here shares, and the PTY dependency this package
// uses does not implement it: its Windows build returns "unsupported" for both
// allocating a terminal and resizing one. Wiring ConPTY by hand would mean a
// second, Windows-only implementation of process start, resize and teardown,
// tested on a platform this project does not build for release today.
//
// So the feature says so rather than half-working. The control is absent, the
// pane explains why in one sentence, and nothing about a Windows build is
// worse than it was: kubectl in the operator's own terminal was always going
// to be the answer there, and it needs nothing PodSteer provides — Windows
// hands a GUI process the same PATH it hands a console one, which is why the
// login-shell PATH adoption is a no-op there too.
//
// Deliberately in its own file behind a build tag, so the PTY dependency is
// not even linked into the Windows binary.
type ptyProcess struct{}

// errUnsupported is what every entry point here returns.
var errUnsupported = errors.New(UnsupportedNotice)

func supported() (bool, string) { return false, UnsupportedNotice }

func startPTY(*exec.Cmd, uint16, uint16) (*ptyProcess, error) { return nil, errUnsupported }

func (p *ptyProcess) Read([]byte) (int, error)  { return 0, errUnsupported }
func (p *ptyProcess) Write([]byte) (int, error) { return 0, errUnsupported }
func (p *ptyProcess) Resize(uint16, uint16) error {
	return errUnsupported
}
func (p *ptyProcess) Hangup()     {}
func (p *ptyProcess) Kill()       {}
func (p *ptyProcess) Wait() error { return errUnsupported }
func (p *ptyProcess) Close()      {}
