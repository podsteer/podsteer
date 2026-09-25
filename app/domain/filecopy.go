package domain

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// Copying files to and from a container is `kubectl cp` and nothing more:
// a tar stream over an exec session, packed on one side and unpacked on the
// other. The rules here are the ones that decide what that stream may do to
// the OPERATOR'S machine, which is why they live in the domain rather than in
// the adapter that writes the files — a path-traversal check is a rule to
// argue with in a test, not an implementation detail of archive/tar.

var (
	// ErrInvalidRemotePath means a container path names nothing a copy can
	// work with: empty, or the root of the filesystem itself. The root is
	// refused rather than attempted because `tar cf - -C / /` archives the
	// whole container under stripped names, which is never what somebody
	// meant by "download this".
	ErrInvalidRemotePath = errors.New("invalid container path")

	// ErrUnsafeArchiveEntry means an entry in a tar stream would land, or
	// point, outside the directory the operator chose. The stream comes from
	// a container the operator does not necessarily control — an image pulled
	// from a registry, a workload somebody else wrote — so an entry named
	// `../../.ssh/authorized_keys`, or a symlink to `/etc`, is treated as an
	// attack on this machine and ends the transfer, never as a file to skip.
	ErrUnsafeArchiveEntry = errors.New("archive entry would escape the chosen directory")

	// ErrTransferTooLarge means a transfer crossed TransferLimits. A stream
	// has no size header, so the cap is checked as bytes and entries arrive;
	// whatever had already landed before the cap stays, and the error says
	// which limit stopped it so an operator who meant it can raise the limit
	// rather than guess.
	ErrTransferTooLarge = errors.New("transfer exceeds the configured limit")
)

// Default ceilings for one transfer. Both exist because a stream from a
// container is unbounded by construction: nothing stops `tar cf - /` inside
// a busy container from producing gigabytes, and nothing stops an image from
// carrying a hundred thousand tiny files. A gigabyte and a hundred thousand
// entries is far above what somebody copies out of a container by hand and
// far below what fills a laptop's disk.
const (
	DefaultTransferMaxBytes   int64 = 1 << 30
	DefaultTransferMaxEntries       = 100_000
)

// TransferLimits caps one copy in either direction.
//
// A zero field means the default, so a caller with no opinion passes the
// zero value and gets a bounded transfer rather than an unbounded one.
type TransferLimits struct {
	// MaxBytes is the most file content one transfer may carry.
	MaxBytes int64
	// MaxEntries is the most archive entries — files, directories and
	// links together — one transfer may carry.
	MaxEntries int
}

// WithDefaults returns the limits with every zero field filled in.
func (l TransferLimits) WithDefaults() TransferLimits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = DefaultTransferMaxBytes
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = DefaultTransferMaxEntries
	}
	return l
}

// TransferSummary is what one copy actually moved, as the operator is told
// once it finishes.
type TransferSummary struct {
	// Entries is every archive entry handled: files, directories and links.
	Entries int
	// Files is the regular files among them — the figure somebody actually
	// means by "how many files came across".
	Files int
	// Bytes is the file content transferred, excluding tar's own headers.
	Bytes int64
	// Notes records what was deliberately left out — a device node, a
	// hard link to something outside the selection — one line each, so a
	// transfer that quietly dropped something is never reported as complete
	// without saying what it dropped.
	Notes []string
}

// SplitRemotePath turns a container path into the directory tar runs in and
// the entry it archives, so that `tar cf - -C dir base` produces entries
// rooted at the path's own name rather than at its full absolute path —
// which is what makes `/etc/nginx` land as `nginx/…` under the chosen local
// directory instead of `etc/nginx/…`.
//
// A relative path is allowed and resolved by tar against the container's
// working directory, exactly as `kubectl cp` does; the frontend shows that
// directory as the hint for this reason. A path that cleans to the
// filesystem root is refused — see ErrInvalidRemotePath.
func SplitRemotePath(remote string) (dir, base string, err error) {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return "", "", fmt.Errorf("%w: no path given", ErrInvalidRemotePath)
	}
	// A NUL cannot appear in a path on any filesystem Kubernetes runs on,
	// and it is how an argument gets truncated on its way through an exec.
	if strings.ContainsRune(trimmed, 0) {
		return "", "", fmt.Errorf("%w: %q contains a NUL byte", ErrInvalidRemotePath, remote)
	}

	cleaned := path.Clean(trimmed)
	if cleaned == "/" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", fmt.Errorf("%w: %q must name a file or directory inside the container", ErrInvalidRemotePath, remote)
	}

	dir, base = path.Split(cleaned)
	if dir == "" {
		// A bare name is relative to wherever the shell would start, which
		// tar spells as ".".
		return ".", base, nil
	}
	// path.Split leaves the trailing slash on the directory; tar does not
	// care, but the command an operator reads in a log should say `-C /etc`
	// rather than `-C /etc/`.
	return path.Clean(dir), base, nil
}

// CleanRemoteDir is the destination of an upload, cleaned the same way
// SplitRemotePath cleans a source. The root IS allowed here: uploading into
// `/` is unusual but well-defined, and tar extracts into it exactly as into
// any other directory.
func CleanRemoteDir(remote string) (string, error) {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return "", fmt.Errorf("%w: no destination given", ErrInvalidRemotePath)
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", fmt.Errorf("%w: %q contains a NUL byte", ErrInvalidRemotePath, remote)
	}
	return path.Clean(trimmed), nil
}
