package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Copying files is `kubectl cp`'s mechanism and nothing else: a tar stream
// over a non-TTY exec session, with tar running INSIDE the container. That
// choice is what makes the feature work against any cluster with no agent,
// no privileged access and no dependency beyond the exec permission a shell
// already needs — and it is what makes an image without tar the ordinary
// failure, reported by name below rather than as an exit code.

// stderrCap bounds what is kept of a command's stderr. Tar's diagnostics are
// a few lines; a container that writes megabytes to stderr is misbehaving,
// and its output is not worth holding in memory to quote back.
const stderrCap = 64 * 1024

// stderrExcerpt is how much of that reaches an error message — the FIRST
// part, because tar names what went wrong before it says it is giving up.
const stderrExcerpt = 1024

// downloadCommand is the command a download runs in the container.
//
// `-C dir base` rather than the full path, so the archive's entries are
// named `base/…` and land under the chosen local directory by their own
// name — see domain.SplitRemotePath.
func downloadCommand(dir, base string) []string {
	return []string{"tar", "cf", "-", "-C", dir, base}
}

// uploadCommand is the command an upload runs in the container: extract
// whatever arrives on stdin into dest.
func uploadCommand(dest string) []string {
	return []string{"tar", "xf", "-", "-C", dest}
}

// CopyFromPod streams `tar cf -` of one container path to out. See
// ports.ManagementPort.
func (a *Adapter) CopyFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remotePath string, out io.Writer) error {
	dir, base, err := domain.SplitRemotePath(remotePath)
	if err != nil {
		return err
	}

	op := fmt.Sprintf("copying %s from %s/%s in %q", remotePath, namespace, podName, id)
	stderr := newCappedBuffer(stderrCap)

	err = a.runCommand(ctx, id, namespace, podName, containerName, downloadCommand(dir, base), nil, out, stderr)
	return a.commandOutcome(ctx, op, err, stderr.String())
}

// CopyToPod streams in into `tar xf - -C dir` in the container. See
// ports.ManagementPort.
func (a *Adapter) CopyToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remoteDir string, in io.Reader) error {
	dest, err := domain.CleanRemoteDir(remoteDir)
	if err != nil {
		return err
	}

	op := fmt.Sprintf("copying into %s on %s/%s in %q", remoteDir, namespace, podName, id)
	stderr := newCappedBuffer(stderrCap)

	// No stdout: tar xf prints nothing on success, and asking for a stream
	// nobody reads would only give a misbehaving image somewhere to write.
	err = a.runCommand(ctx, id, namespace, podName, containerName, uploadCommand(dest), in, nil, stderr)
	return a.commandOutcome(ctx, op, err, stderr.String())
}

// runCommand executes command without a TTY, wiring whichever streams are
// non-nil, and returns the stream's own error untouched for commandOutcome
// to read.
func (a *Adapter) runCommand(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	exec, err := a.execCommand(ctx, id, namespace, podName, containerName, command, stdin != nil, stdout != nil, stderr != nil, false)
	if err != nil {
		return err
	}

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    false,
	})
}

// commandOutcome turns what an exec returned, plus what the command wrote
// to stderr, into the error a caller can act on.
//
// STDERR IS NEVER DISCARDED. On failure it is the diagnosis and travels in
// the error verbatim; on success it is logged, because tar's warnings —
// "Removing leading `/' from member names" — say something about the
// transfer even when the exit status does not.
func (a *Adapter) commandOutcome(ctx context.Context, op string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)

	if err == nil {
		if stderr != "" {
			a.logger.Warn("command wrote to stderr",
				slog.String("op", op),
				slog.String("stderr", excerpt(stderr, stderrExcerpt)))
		}
		return nil
	}

	// The operator's Cancel, or the window closing: the command was killed
	// on purpose, and whatever it said on the way out is not a diagnosis.
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", op, ctx.Err())
	}

	if tarMissing(err, stderr) {
		return fmt.Errorf("%s: %w: %s", op, ports.ErrTarMissing, excerpt(firstNonEmpty(stderr, err.Error()), stderrExcerpt))
	}

	// A non-zero exit is the command's own verdict, and tar's stderr is its
	// wording of it. Kept apart from classify: an exit status is not a
	// transport failure, and reporting "tar: /nope: Cannot stat" as the
	// cluster being unreachable would send somebody to check a VPN.
	var exit utilexec.ExitError
	if errors.As(err, &exit) {
		detail := stderr
		if detail == "" {
			detail = fmt.Sprintf("exit status %d", exit.ExitStatus())
		}
		return fmt.Errorf("%s: %w: %s", op, ports.ErrCommandFailed, excerpt(detail, stderrExcerpt))
	}

	classified := classify(op, err)
	if stderr != "" {
		return fmt.Errorf("%w: %s", classified, excerpt(stderr, stderrExcerpt))
	}
	return classified
}

// tarMissing reports whether a failure means the container has no tar.
//
// There is no one signal for it. A runtime that cannot start the process
// answers the exec with an internal error whose message says "executable
// file not found"; a shell wrapper exits 127 and writes "tar: not found";
// and the wording differs between containerd, CRI-O and busybox. All of
// them are matched, because the alternative — an operator reading "exit
// status 127" and searching for what it means — is the experience every
// other client gives for this and the one this sentinel exists to end.
func tarMissing(err error, stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, phrase := range []string{"tar: not found", "tar: command not found", "tar: no such file or directory"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "tar") &&
		(strings.Contains(message, "executable file not found") || strings.Contains(message, "no such file or directory")) {
		return true
	}

	var exit utilexec.ExitError
	return errors.As(err, &exit) && exit.ExitStatus() == 127
}

// cappedBuffer keeps the first cap bytes written to it and drops the rest,
// reporting every write as fully consumed so the writer never fails.
type cappedBuffer struct {
	cap  int
	data []byte
}

func newCappedBuffer(cap int) *cappedBuffer {
	return &cappedBuffer{cap: cap}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	// The full length is reported whatever was kept: a short write would
	// make io.Copy stop the stream over diagnostics nobody needed.
	n := len(p)
	if room := b.cap - len(b.data); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *cappedBuffer) String() string {
	return string(b.data)
}

// excerpt truncates text to at most n bytes, marking the cut.
func excerpt(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n] + "…"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
