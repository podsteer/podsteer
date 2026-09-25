//go:build !windows

package vendorcli

import (
	"os/exec"
	"syscall"
	"time"
)

// killGrace is how long the tree is given to stop politely before the group is
// killed outright. The same shape as the local shell's hangup grace: long
// enough for a CLI to finish a write it had started, short enough that nobody
// notices.
const killGrace = 2 * time.Second

// setProcessGroup puts the child in a group of its own, so a timeout can stop
// what it started as well.
//
// THE GRANDCHILDREN ARE THE POINT. These CLIs shell out — to a credential
// helper, to a browser — and killing only the leader leaves those running with
// nothing waiting for them. A process group is the one handle that reaches
// them all.
//
// WaitDelay is what makes the timeout real: without it, Run waits for stdout
// to close, and a grandchild holding the pipe open keeps PodSteer waiting long
// after the process it started was killed.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		group := -cmd.Process.Pid

		// TERM, NOT INT, and the difference is measurable rather than a
		// preference: a POSIX shell sets SIGINT to IGNORED in the
		// asynchronous subshells it starts, so a CLI that backgrounded a
		// credential helper would have the helper survive an interrupt of the
		// whole group. The test that pins this watches for a grandchild
		// outliving the run, and it did.
		//
		// Still not KILL first: these CLIs write files, and one killed
		// mid-write leaves a half-written kubeconfig PodSteer would then read.
		if err := syscall.Kill(group, syscall.SIGTERM); err != nil {
			return cmd.Process.Kill()
		}

		// And then the group again, harder, for anything that ignored it.
		// os/exec's WaitDelay reaches only the leader, which is precisely the
		// process that was never the problem.
		go func() {
			time.Sleep(killGrace)
			_ = syscall.Kill(group, syscall.SIGKILL)
		}()
		return nil
	}
}
