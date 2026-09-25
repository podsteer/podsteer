package k8s

import (
	"context"
	"fmt"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// listingOutputCap is the most a listing may print before it is refused.
//
// A MEGABYTE, AND REFUSED RATHER THAN TRUNCATED. This is output that gets
// DECODED, which is the other of the two bounding patterns this package uses:
// stderr is capped and kept because it is quoted, but a listing read from a
// truncated stream would be a directory silently missing its tail. The script
// caps itself at ListingEntryCap; this is what stands between PodSteer and a
// container that ignored it.
const listingOutputCap = 1 << 20

// ListDirectory lists one directory inside a container.
//
// STRUCTURALLY THE PROBE, not the file copy: one exec, one shot, bounded
// output, and a script the domain composed. Nothing here decides what a
// listing means — see domain/filebrowse.go, where the command and the parser
// live together because they are one protocol.
func (a *Adapter) ListDirectory(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
	podName, containerName, remoteDir string,
) (domain.DirectoryListing, error) {
	const op = "listing a directory"

	dir, err := domain.CleanListPath(remoteDir)
	if err != nil {
		return domain.DirectoryListing{}, err
	}

	stdout := newCappedBuffer(listingOutputCap)
	stderr := newCappedBuffer(stderrCap)

	runErr := a.runCommand(ctx, id, namespace, podName, containerName,
		domain.ListCommand(dir), nil, stdout, stderr)

	if runErr != nil {
		// A CONTAINER WITH NO SHELL IS A STATE, NOT A FAULT. Checked before
		// commandOutcome, which would otherwise classify the exec's own error
		// as a command that failed — sending somebody to read a shell's
		// complaint that no shell wrote.
		if shellMissing(runErr, stderr.String()) {
			return domain.DirectoryListing{}, fmt.Errorf("%s: %w", op, ports.ErrShellMissing)
		}
		return domain.DirectoryListing{}, a.commandOutcome(ctx, op, runErr, stderr.String())
	}

	if stdout.Len() >= listingOutputCap {
		return domain.DirectoryListing{}, fmt.Errorf("%s: %w", op, domain.ErrListingUnreadable)
	}

	listing, ok, parseErr := domain.ParseListOutput(stdout.String(), dir)
	if !ok {
		if parseErr != nil {
			// The script's own refusals — no shell, or a directory the
			// container's user cannot read — arrive as a cause rather than a
			// sentinel, exactly as the probe's `unsupported` line does.
			return domain.DirectoryListing{}, fmt.Errorf("%s: %w: %s",
				op, ports.ErrCommandFailed, parseErr.Error())
		}
		return domain.DirectoryListing{}, fmt.Errorf("%s: %w", op, domain.ErrListingUnreadable)
	}

	return listing, nil
}
