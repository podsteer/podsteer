package application_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
)

// collectingPublisher records what was published, safely across the watcher's
// goroutine and the test's.
type collectingPublisher struct {
	mu     sync.Mutex
	events []domain.DomainEvent
}

func (p *collectingPublisher) Publish(_ context.Context, event domain.DomainEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *collectingPublisher) all() []domain.DomainEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]domain.DomainEvent(nil), p.events...)
}

func writeKubeconfigFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// startWatcher runs one on a short interval and stops it with the test.
func startWatcher(t *testing.T, files func() []string, events *collectingPublisher) {
	t.Helper()

	watcher, err := application.NewKubeconfigWatcher(application.KubeconfigWatcherDeps{
		Files:    files,
		Events:   events,
		Interval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewKubeconfigWatcher() error = %v", err)
	}
	watcher.Start(context.Background())
	t.Cleanup(watcher.Stop)
}

// waitForEvents fails the test if the count is not reached in time.
func waitForEvents(t *testing.T, events *collectingPublisher, want int) []domain.DomainEvent {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := events.all(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("published %d events, want %d", len(events.all()), want)
	return nil
}

// TestKubeconfigWatcherSaysNothingWhenNothingChanges is the property that has
// to hold on every tick of a running application: a watcher that cries change
// on a quiet disk would make the interface re-read the world for ever.
func TestKubeconfigWatcherSaysNothingWhenNothingChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	writeKubeconfigFile(t, path, "apiVersion: v1\nkind: Config\n")

	events := &collectingPublisher{}
	startWatcher(t, func() []string { return []string{path} }, events)

	// Several intervals of a file nobody is touching.
	time.Sleep(200 * time.Millisecond)

	if got := events.all(); len(got) != 0 {
		t.Fatalf("published %d events for an unchanged file, want none", len(got))
	}
}

// TestKubeconfigWatcherNoticesAnEditedFile covers the ordinary case: somebody
// runs `kubectl config use-context` in another window.
func TestKubeconfigWatcherNoticesAnEditedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	writeKubeconfigFile(t, path, "apiVersion: v1\nkind: Config\n")

	events := &collectingPublisher{}
	startWatcher(t, func() []string { return []string{path} }, events)

	// A size change, which is what an edit is. (Modification time alone is
	// second-resolution on some filesystems, so the assertion does not rest
	// on it.)
	writeKubeconfigFile(t, path, "apiVersion: v1\nkind: Config\ncurrent-context: dev\n")

	published := waitForEvents(t, events, 1)
	change, ok := published[0].(domain.KubeconfigChanged)
	if !ok {
		t.Fatalf("published %T, want domain.KubeconfigChanged", published[0])
	}
	if change.Files != 1 {
		t.Errorf("Files = %d, want 1", change.Files)
	}
}

// TestKubeconfigWatcherNoticesAFileAppearingInAFolder is the case a
// file-system watcher makes hardest and a fingerprint makes free: a source is
// a FOLDER, and what changed is that something new is in it.
func TestKubeconfigWatcherNoticesAFileAppearingInAFolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "a.yaml")
	writeKubeconfigFile(t, first, "apiVersion: v1\nkind: Config\n")

	// The composed list is what the adapter would report: everything in the
	// folder, re-scanned on every call.
	files := func() []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		var paths []string
		for _, entry := range entries {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
		return paths
	}

	events := &collectingPublisher{}
	startWatcher(t, files, events)

	writeKubeconfigFile(t, filepath.Join(dir, "b.yaml"), "apiVersion: v1\nkind: Config\n")

	published := waitForEvents(t, events, 1)
	if change := published[0].(domain.KubeconfigChanged); change.Files != 2 {
		t.Errorf("Files = %d, want 2 — the new file is part of the set", change.Files)
	}
}

// TestKubeconfigWatcherNoticesAFileDisappearing is why a file that cannot be
// stat'd is recorded as missing rather than skipped.
//
// Skipping would make a delete invisible, and a restore invisible too — which
// is precisely what a sync client replacing a folder looks like.
func TestKubeconfigWatcherNoticesAFileDisappearing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	writeKubeconfigFile(t, path, "apiVersion: v1\nkind: Config\n")

	events := &collectingPublisher{}
	startWatcher(t, func() []string { return []string{path} }, events)

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing: %v", err)
	}

	waitForEvents(t, events, 1)
}

// TestKubeconfigWatcherIsSilentOnItsFirstLook keeps start-up quiet: the files
// at launch are simply what they are, and an event then would tell the
// interface that the list it has just read is stale.
func TestKubeconfigWatcherIsSilentOnItsFirstLook(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	writeKubeconfigFile(t, path, "apiVersion: v1\nkind: Config\n")

	events := &collectingPublisher{}
	startWatcher(t, func() []string { return []string{path} }, events)

	time.Sleep(100 * time.Millisecond)
	if got := events.all(); len(got) != 0 {
		t.Fatalf("published %d events before anything changed", len(got))
	}
}

// TestKubeconfigWatcherStopWaitsForItsGoroutine so shutdown cannot leave one
// stat'ing files after the application says it has stopped.
func TestKubeconfigWatcherStopWaitsForItsGoroutine(t *testing.T) {
	t.Parallel()

	watcher, err := application.NewKubeconfigWatcher(application.KubeconfigWatcherDeps{
		Files:    func() []string { return nil },
		Events:   &collectingPublisher{},
		Interval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewKubeconfigWatcher() error = %v", err)
	}

	watcher.Start(context.Background())
	done := make(chan struct{})
	go func() {
		watcher.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return, so its goroutine is still running")
	}

	// Stopping twice is a no-op rather than a panic on a closed channel: the
	// shutdown path and a test cleanup can both reach it.
	watcher.Stop()
}
