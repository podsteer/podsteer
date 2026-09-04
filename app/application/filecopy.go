package application

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Copying a file to or from a container is two halves joined by a pipe: the
// exec session the ManagementPort drives, and the local pack or unpack the
// ArchivePort performs. This file is the join. Neither half knows about the
// other — the adapter streams bytes, the archive reads or writes them — and
// what this service adds is the read-only guard on the direction that
// writes into the cluster, the audit line every transfer leaves behind, and
// the rule for which of two simultaneous failures is the one worth
// reporting.

// errFileCopyUnavailable is returned when no ArchivePort was wired. A
// programming error in the composition root, never something an operator
// can cause, so it is not classified further.
var errFileCopyUnavailable = errors.New("file copy is not available: no archive implementation is wired")

// errTransferStopped is what this side of the pipe is closed with once the
// other side has finished or failed, so a goroutine still moving bytes sees
// a distinctive error rather than a generic closed pipe — and so
// transferOutcome can tell a failure from its consequence.
var errTransferStopped = errors.New("transfer stopped")

// DownloadFromPod copies one file or directory out of a container into
// localDir, a directory the operator chose.
//
// ALLOWED ON A READ-ONLY CLUSTER. Reading a file out of a container changes
// nothing about the cluster, exactly as streaming its logs changes nothing,
// and the guard has no business refusing it — see refuseIfReadOnly's own
// comment on the reads that live beside it. The exec permission is the
// cluster's to grant or refuse.
//
// progress, when non-nil, receives the size of each write to disk.
func (s *ManagementService) DownloadFromPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remotePath, localDir string, progress func(int64)) (domain.TransferSummary, error) {
	if s.archive == nil {
		return domain.TransferSummary{}, errFileCopyUnavailable
	}
	// Refused here, before an exec is opened for a path tar would refuse
	// anyway — and so an invalid path costs no round trip.
	if _, _, err := domain.SplitRemotePath(remotePath); err != nil {
		return domain.TransferSummary{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	reader, writer := io.Pipe()
	execDone := make(chan error, 1)

	// The exec has a defined owner and exit: this goroutine ends when the
	// stream does, and the stream ends when the container finishes, the
	// reader stops, or ctx is cancelled — every one of which is arranged
	// below before this function returns.
	go func() {
		err := s.management.CopyFromPod(ctx, id, namespace, podName, containerName, remotePath, writer)
		// A nil closes cleanly, which the reader sees as the archive's end;
		// an error reaches the reader as its next read.
		_ = writer.CloseWithError(err)
		execDone <- err
	}()

	summary, localErr := s.archive.Extract(ctx, reader, localDir, s.limits, progress)
	if localErr != nil {
		// The exec is still streaming whatever is left, and client-go
		// discards output nobody reads: left alone it would drain the
		// entire archive across the network for nothing.
		cancel()
	}
	_ = reader.CloseWithError(errTransferStopped)
	execErr := <-execDone

	err := transferOutcome(execErr, localErr)
	s.auditTransfer(ctx, "download", id, namespace, podName, containerName, remotePath, summary, err)
	return summary, err
}

// UploadToPod copies one local file or directory into remoteDir inside a
// container.
//
// A WRITE INTO THE CLUSTER'S WORKLOAD, and refused on a read-only cluster
// exactly like every other write here: whatever lands in the container is a
// change to a running process's world, and a config file dropped into the
// wrong pod is precisely the mistake the guard exists to catch.
func (s *ManagementService) UploadToPod(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, localPath, remoteDir string, progress func(int64)) (domain.TransferSummary, error) {
	if err := s.refuseIfReadOnly(id); err != nil {
		return domain.TransferSummary{}, err
	}
	if s.archive == nil {
		return domain.TransferSummary{}, errFileCopyUnavailable
	}
	if _, err := domain.CleanRemoteDir(remoteDir); err != nil {
		return domain.TransferSummary{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	reader, writer := io.Pipe()
	type packed struct {
		summary domain.TransferSummary
		err     error
	}
	packDone := make(chan packed, 1)

	go func() {
		summary, err := s.archive.Pack(ctx, writer, localPath, s.limits, progress)
		// A nil closes cleanly — tar in the container sees the end of the
		// archive. An error truncates it, and tar's own complaint about
		// that is the consequence transferOutcome ranks below the cause.
		_ = writer.CloseWithError(err)
		packDone <- packed{summary: summary, err: err}
	}()

	execErr := s.management.CopyToPod(ctx, id, namespace, podName, containerName, remoteDir, reader)
	if execErr != nil {
		// tar exited early; nothing is reading what Pack is producing.
		cancel()
	}
	_ = reader.CloseWithError(errTransferStopped)
	result := <-packDone

	err := transferOutcome(execErr, result.err)
	s.auditTransfer(ctx, "upload", id, namespace, podName, containerName, remoteDir, summaryOrZero(result.summary), err)
	return result.summary, err
}

// transferOutcome picks the error worth reporting when the two halves of a
// pipe both failed — which they routinely do, because one side stopping
// makes the other side fail a moment later.
//
// The order is by how much the operator can do with the answer. A missing
// tar is the whole diagnosis. A limit or an unsafe entry is a decision this
// process made, and the exec's failure is a consequence of it. Otherwise
// the exec's own error wins unless it is merely this side having stopped
// it, in which case whatever stopped it is the cause.
func transferOutcome(execErr, localErr error) error {
	switch {
	case execErr != nil && errors.Is(execErr, ports.ErrTarMissing):
		return execErr
	case localErr != nil && (errors.Is(localErr, domain.ErrTransferTooLarge) || errors.Is(localErr, domain.ErrUnsafeArchiveEntry)):
		return localErr
	case execErr != nil && !errors.Is(execErr, errTransferStopped) && !errors.Is(execErr, io.ErrClosedPipe) && !errors.Is(execErr, context.Canceled):
		return execErr
	case localErr != nil:
		return localErr
	default:
		return execErr
	}
}

// auditTransfer writes the one line every transfer leaves in the log.
//
// Cluster, namespace, pod, container, the container-side path, the
// direction and the byte count — and NEVER a file's contents, which is why
// nothing here has access to them: the bytes went through a pipe this
// method never saw. The local path is not recorded either; where somebody
// keeps their downloads is a fact about their machine, not about the
// cluster this log describes.
func (s *ManagementService) auditTransfer(ctx context.Context, direction string, id domain.ClusterID, namespace domain.NamespaceName, podName, containerName, remotePath string, summary domain.TransferSummary, err error) {
	attrs := []slog.Attr{
		slog.String("direction", direction),
		slog.String("cluster", id.String()),
		slog.String("namespace", namespace.String()),
		slog.String("pod", podName),
		slog.String("container", containerName),
		slog.String("remotePath", remotePath),
		slog.Int64("bytes", summary.Bytes),
		slog.Int("files", summary.Files),
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
		s.logger.LogAttrs(ctx, slog.LevelError, "file transfer failed", attrs...)
		return
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "file transfer finished", attrs...)
}

func summaryOrZero(summary domain.TransferSummary) domain.TransferSummary {
	return summary
}
