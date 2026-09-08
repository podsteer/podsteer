//go:build !windows

package localshell

import (
	"os"
	"runtime"
)

// defaultLoginShell names the shell to run when nothing else says.
//
// $SHELL is the operator's own answer and is preferred whenever it exists;
// the fallbacks are only for a process launched with no environment at all.
func defaultLoginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// loginShellArgs is what turns the shell above into a LOGIN shell.
//
// `-l` alone is both login AND interactive: a shell with no command argument,
// on a terminal, decides it is interactive by itself. That is what makes it
// read the operator's own startup files and draw their own prompt, rather
// than being a stripped shell wearing their PATH.
func loginShellArgs() []string { return []string{"-l"} }
