//go:build windows

package vendorcli

import (
	"os/exec"
	"time"
)

// setProcessGroup bounds the wait on Windows.
//
// There is no process group to signal here — that is Windows' model, and
// pretending otherwise in a comment would be worse than saying it — so a
// timeout kills the child and WaitDelay stops PodSteer waiting on a pipe a
// grandchild is still holding open.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
