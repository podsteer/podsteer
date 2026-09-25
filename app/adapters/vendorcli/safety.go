package vendorcli

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/podsteer/podsteer/app/ports"
)

// resolve finds a binary on PATH, and refuses anything that is not one.
//
// THREE REFUSALS, EACH FOR A DIFFERENT MISTAKE:
//
//   - exec.ErrDot, which is Go refusing to resolve a program from the current
//     directory. A desktop application's working directory is not somewhere an
//     operator chose, and running something out of it is how a downloaded
//     folder becomes a code path.
//   - a path that is not absolute after resolution, for the same reason.
//   - anything that did not come from PATH at all. There is deliberately no
//     way to configure a path to these binaries: a setting naming a program to
//     run is a setting that runs any program.
func resolve(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if errors.Is(err, exec.ErrDot) {
		return "", fmt.Errorf("%w: %q resolved to the working directory", ports.ErrVendorCLIMissing, binary)
	}
	if err != nil {
		return "", fmt.Errorf("%w: %q", ports.ErrVendorCLIMissing, binary)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q did not resolve to an absolute path", ports.ErrVendorCLIMissing, binary)
	}
	return path, nil
}
