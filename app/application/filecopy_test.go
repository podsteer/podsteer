package application_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeArchive plays the local filesystem's part without touching one.
//
// Extract drains what it is given and remembers it; Pack writes a canned
// payload. Either can be made to fail before moving a byte, which is how
// the tests below reproduce a limit tripping or a hostile entry — the
// decisions the real implementation makes, exercised here only for what
// the service does about them.
type fakeArchive struct {
	mu sync.Mutex

	extractErr     error
	extractCalled  bool
	extractDest    string
	extractLimits  domain.TransferLimits
	extracted      []byte
	extractSummary domain.TransferSummary

	packErr     error
	packPayload []byte
	packCalled  bool
	packSource  string
	packSummary domain.TransferSummary
}

var _ ports.ArchivePort = (*fakeArchive)(nil)

func (f *fakeArchive) Extract(_ context.Context, r io.Reader, dest string, limits domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error) {
	f.mu.Lock()
	f.extractCalled = true
	f.extractDest = dest
	f.extractLimits = limits
	failure := f.extractErr
	f.mu.Unlock()

	if failure != nil {
		return domain.TransferSummary{}, failure
	}

	data, err := io.ReadAll(r)
	f.mu.Lock()
	f.extracted = data
	f.mu.Unlock()
	if err != nil {
		return domain.TransferSummary{}, err
	}
	if progress != nil {
		progress(int64(len(data)))
	}
	return f.extractSummary, nil
}

func (f *fakeArchive) Pack(_ context.Context, w io.Writer, source string, _ domain.TransferLimits, progress func(int64)) (domain.TransferSummary, error) {
	f.mu.Lock()
	f.packCalled = true
	f.packSource = source
	failure, payload := f.packErr, f.packPayload
	f.mu.Unlock()

	if failure != nil {
		return domain.TransferSummary{}, failure
	}
	if _, err := w.Write(payload); err != nil {
		return domain.TransferSummary{}, err
	}
	if progress != nil {
		progress(int64(len(payload)))
	}
	return f.packSummary, nil
}

func newFileCopyService(t *testing.T, management *fakeManagementPort, archive ports.ArchivePort, registry *application.Registry, logger *slog.Logger) *application.ManagementService {
	t.Helper()

	service, err := application.NewManagementService(application.ManagementServiceDeps{
		Management:     management,
		Registry:       registry,
		Logger:         logger,
		Archive:        archive,
		TransferLimits: domain.TransferLimits{MaxBytes: 1234, MaxEntries: 5},
	})
	if err != nil {
		t.Fatalf("NewManagementService() error = %v", err)
	}
	return service
}

// TestDownloadFromPodStreamsTheArchiveIntoTheExtractor is the happy path:
// what the exec wrote is exactly what the archive received, at the chosen
// destination, under the configured limits.
func TestDownloadFromPodStreamsTheArchiveIntoTheExtractor(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{copyFromPayload: []byte("ARCHIVE-BYTES")}
	archive := &fakeArchive{extractSummary: domain.TransferSummary{Files: 3, Bytes: 13}}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	summary, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/etc/nginx", "/Users/me/Downloads", nil)
	if err != nil {
		t.Fatalf("DownloadFromPod() error = %v", err)
	}

	if string(archive.extracted) != "ARCHIVE-BYTES" {
		t.Fatalf("archive received %q, want the exec's stream", archive.extracted)
	}
	if archive.extractDest != "/Users/me/Downloads" {
		t.Fatalf("destination = %q", archive.extractDest)
	}
	if archive.extractLimits.MaxBytes != 1234 || archive.extractLimits.MaxEntries != 5 {
		t.Fatalf("limits = %+v, want the configured ones", archive.extractLimits)
	}
	if summary.Files != 3 {
		t.Fatalf("summary = %+v, want the archive's own", summary)
	}
	if management.copyFromRemote != "/etc/nginx" || management.copyFromContainer != "app" || management.copyFromPod != "api-0" {
		t.Fatalf("adapter saw %q/%q/%q", management.copyFromPod, management.copyFromContainer, management.copyFromRemote)
	}
}

// TestDownloadFromPodIsAllowedOnAReadOnlyCluster: reading a file out is a
// read, and the guard must not refuse it.
func TestDownloadFromPodIsAllowedOnAReadOnlyCluster(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management := &fakeManagementPort{copyFromPayload: []byte("x")}
	archive := &fakeArchive{}
	service := newFileCopyService(t, management, archive, registry, nil)

	if _, err := service.DownloadFromPod(context.Background(), "prod", "web", "api-0", "app", "/etc/hosts", "/tmp", nil); err != nil {
		t.Fatalf("DownloadFromPod() on a read-only cluster error = %v, want nil", err)
	}
	if !management.copyFromCalled {
		t.Fatal("the download never reached the adapter")
	}
}

// TestUploadToPodRefusesWhenReadOnly is UploadToPod's share of the property
// every write here holds: refused before the adapter or the archive is
// touched.
func TestUploadToPodRefusesWhenReadOnly(t *testing.T) {
	t.Parallel()

	registry := application.NewRegistry()
	registry.SetReadOnly("prod", true)

	management := &fakeManagementPort{}
	archive := &fakeArchive{packPayload: []byte("PACKED")}
	service := newFileCopyService(t, management, archive, registry, nil)

	_, err := service.UploadToPod(context.Background(), "prod", "web", "api-0", "app", "/Users/me/config.yaml", "/app", nil)
	if !errors.Is(err, ports.ErrReadOnly) {
		t.Fatalf("UploadToPod() error = %v, want ErrReadOnly", err)
	}
	if management.copyToCalled {
		t.Error("UploadToPod() reached the adapter on a read-only cluster")
	}
	if archive.packCalled {
		t.Error("UploadToPod() packed the local path on a read-only cluster")
	}
}

// TestUploadToPodStreamsThePackedArchiveIntoTheContainer: the container
// receives exactly what the archive packed.
func TestUploadToPodStreamsThePackedArchiveIntoTheContainer(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	archive := &fakeArchive{packPayload: []byte("PACKED-BYTES"), packSummary: domain.TransferSummary{Files: 1, Bytes: 12}}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	summary, err := service.UploadToPod(context.Background(), "staging", "web", "api-0", "app", "/Users/me/config.yaml", "/app/", nil)
	if err != nil {
		t.Fatalf("UploadToPod() error = %v", err)
	}
	if string(management.copyToReceived) != "PACKED-BYTES" {
		t.Fatalf("container received %q", management.copyToReceived)
	}
	if management.copyToRemote != "/app/" || management.copyToContainer != "app" {
		t.Fatalf("adapter saw %q in %q", management.copyToRemote, management.copyToContainer)
	}
	if archive.packSource != "/Users/me/config.yaml" {
		t.Fatalf("packed %q", archive.packSource)
	}
	if summary.Files != 1 {
		t.Fatalf("summary = %+v", summary)
	}
}

// TestDownloadFromPodReportsAMissingTarOverTheLocalConsequence: the exec
// failing for lack of tar makes the archive fail a moment later on an
// empty stream, and the missing tar is the one worth telling.
func TestDownloadFromPodReportsAMissingTarOverTheLocalConsequence(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{copyFromErr: ports.ErrTarMissing}
	archive := &fakeArchive{}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	_, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/etc/hosts", "/tmp", nil)
	if !errors.Is(err, ports.ErrTarMissing) {
		t.Fatalf("DownloadFromPod() error = %v, want ErrTarMissing", err)
	}
}

// TestDownloadFromPodReportsTheLimitOverTheExecsConsequence is the other
// direction: the archive stops at its cap, the exec's stream is cut off
// under it, and the cap is the cause.
func TestDownloadFromPodReportsTheLimitOverTheExecsConsequence(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{copyFromPayload: bytes.Repeat([]byte("x"), 4096)}
	archive := &fakeArchive{extractErr: domain.ErrTransferTooLarge}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	_, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/var/log", "/tmp", nil)
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("DownloadFromPod() error = %v, want ErrTransferTooLarge", err)
	}
}

// TestUploadToPodReportsTheExecsFailureWhenTarRefusesTheDestination: tar
// exits before reading anything, the packer's writes fail as a result,
// and the exec's own error is what comes back.
func TestUploadToPodReportsTheExecsFailureWhenTarRefusesTheDestination(t *testing.T) {
	t.Parallel()

	refused := errors.New("tar: /nope: Cannot open: No such file or directory")
	management := &fakeManagementPort{copyToErr: refused}
	archive := &fakeArchive{packPayload: bytes.Repeat([]byte("y"), 4096)}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	_, err := service.UploadToPod(context.Background(), "dev", "web", "api-0", "app", "/Users/me/dir", "/nope", nil)
	if !errors.Is(err, refused) {
		t.Fatalf("UploadToPod() error = %v, want the exec's own failure", err)
	}
}

// TestUploadToPodReportsAPackFailureOverTarsComplaint: a local limit stops
// the packer, tar sees a truncated archive, and the limit is the cause.
func TestUploadToPodReportsAPackFailureOverTarsComplaint(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	archive := &fakeArchive{packErr: domain.ErrTransferTooLarge}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	_, err := service.UploadToPod(context.Background(), "dev", "web", "api-0", "app", "/Users/me/dir", "/app", nil)
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("UploadToPod() error = %v, want ErrTransferTooLarge", err)
	}
}

// TestDownloadFromPodRefusesAnInvalidPathBeforeTheAdapter: the root never
// costs an exec.
func TestDownloadFromPodRefusesAnInvalidPathBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{}
	archive := &fakeArchive{}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	_, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/", "/tmp", nil)
	if !errors.Is(err, domain.ErrInvalidRemotePath) {
		t.Fatalf("DownloadFromPod(\"/\") error = %v, want ErrInvalidRemotePath", err)
	}
	if management.copyFromCalled || archive.extractCalled {
		t.Fatal("an invalid path reached the adapter or the archive")
	}
}

// TestFileCopyRefusesWithoutAnArchive: a service built the way every other
// test builds it — no ArchivePort — refuses rather than panicking.
func TestFileCopyRefusesWithoutAnArchive(t *testing.T) {
	t.Parallel()

	service := newManagementService(t, &fakeManagementPort{}, application.NewRegistry())

	if _, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/etc/hosts", "/tmp", nil); err == nil {
		t.Fatal("DownloadFromPod() with no archive error = nil")
	}
	if _, err := service.UploadToPod(context.Background(), "dev", "web", "api-0", "app", "/tmp/x", "/app", nil); err == nil {
		t.Fatal("UploadToPod() with no archive error = nil")
	}
}

// TestFileCopyLeavesOneAuditLineNamingTheTransfer pins the audit line's
// shape: cluster, namespace, pod, container, container path, direction and
// byte count — and never the local path, which is a fact about the
// operator's machine rather than the cluster.
func TestFileCopyLeavesOneAuditLineNamingTheTransfer(t *testing.T) {
	t.Parallel()

	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))

	management := &fakeManagementPort{copyFromPayload: []byte("abc")}
	archive := &fakeArchive{extractSummary: domain.TransferSummary{Files: 2, Bytes: 3}}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), logger)

	if _, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/etc/nginx", "/Users/me/Secret Downloads", nil); err != nil {
		t.Fatalf("DownloadFromPod() error = %v", err)
	}

	lines := strings.Count(strings.TrimSpace(log.String()), "\n") + 1
	if lines != 1 {
		t.Fatalf("log has %d lines, want exactly one:\n%s", lines, log.String())
	}
	for _, want := range []string{"direction=download", "cluster=dev", "namespace=web", "pod=api-0", "container=app", "remotePath=/etc/nginx", "bytes=3", "files=2"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("audit line lacks %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(log.String(), "Secret Downloads") {
		t.Errorf("audit line records the local path:\n%s", log.String())
	}
}

// TestFileCopyProgressReachesTheCaller: the callback the UI feeds a
// progress bar from is passed through untouched.
func TestFileCopyProgressReachesTheCaller(t *testing.T) {
	t.Parallel()

	management := &fakeManagementPort{copyFromPayload: []byte("twelve bytes")}
	archive := &fakeArchive{}
	service := newFileCopyService(t, management, archive, application.NewRegistry(), nil)

	var seen int64
	if _, err := service.DownloadFromPod(context.Background(), "dev", "web", "api-0", "app", "/f", "/tmp", func(n int64) { seen += n }); err != nil {
		t.Fatalf("DownloadFromPod() error = %v", err)
	}
	if seen != 12 {
		t.Fatalf("progress saw %d bytes, want 12", seen)
	}
}
