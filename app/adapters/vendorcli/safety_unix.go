//go:build !windows

package vendorcli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/podsteer/podsteer/app/ports"
)

// refuseUnsafePath refuses a binary anybody else on the machine could rewrite.
//
// STRICTER THAN ANYTHING ELSE HERE, AND DELIBERATELY SO. client-go runs
// credential plugins with no such check, and that inconsistency is not an
// oversight: this is a new capability, PodSteer chose to run this program
// rather than being told to by a kubeconfig, and the marginal cost is two
// stats. A world-writable directory on PATH is the oldest way to have somebody
// else's program run as you.
//
// The file's own mode and the directory holding it are both checked, because
// either is enough: a binary nobody can write, in a directory anybody can
// write, can simply be replaced.
func refuseUnsafePath(path string) error {
	for _, target := range []string{path, filepath.Dir(path)} {
		info, err := os.Lstat(target)
		if err != nil {
			return fmt.Errorf("%w: %q could not be checked", ports.ErrVendorCLIMissing, target)
		}
		if info.Mode().Perm()&0o002 != 0 {
			return fmt.Errorf("%w: %q is writable by anybody on this machine, so PodSteer will not run it",
				ports.ErrVendorCLIMissing, target)
		}
	}
	return nil
}
