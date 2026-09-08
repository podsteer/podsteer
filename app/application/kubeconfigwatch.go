package application

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// KubeconfigWatcher notices when the kubeconfig PodSteer reads has changed on
// disk, and says so once.
//
// # Why a poll and not a file-system watcher
//
// A watcher is the obvious answer and the wrong one here, for four reasons
// that all point the same way:
//
//   - THE SET IS NOT A FIXED LIST OF FILES. Sources include FOLDERS, and the
//     interesting change is often a file APPEARING in one — a password
//     manager's export, a `kubectl config` from a colleague, a sync client
//     writing a new file. A watcher would have to watch every folder for
//     creations and every file for writes, and re-arm itself whenever the
//     source list changed.
//   - EDITORS DO NOT WRITE FILES, THEY REPLACE THEM. Atomic saves — write a
//     temporary file, rename over the original — are what `kubectl config`,
//     every editor and most sync clients do, and they break inode-based
//     watches in a way that is invisible until somebody's edit stops being
//     noticed. Re-arming after every event is the standard fix and it is more
//     moving parts than the thing being solved.
//   - IT WOULD BE A DEPENDENCY. fsnotify is another supply-chain entry, three
//     platform backends and a licence line, for a signal this application acts
//     on by re-reading a handful of small files.
//   - LATENCY DOES NOT MATTER HERE. A kubeconfig changes when somebody runs a
//     command in another window. Seeing it within a few seconds is
//     indistinguishable from seeing it instantly, and nothing PodSteer does is
//     wrong in the meantime — the list is simply as it was.
//
// So this stats the files, cheaply, on a slow tick. Nothing is opened, nothing
// is parsed, and no content is read: a fingerprint is path, size and
// modification time, which is what changes when any of the four cases above
// happens.
//
// # What it does not do
//
// It does not reload anything. It raises one event and stops there, because
// the decision about what to do with a changed kubeconfig belongs to the
// interface: re-reading the list is cheap, but a cluster the operator has open
// must not be disturbed by a file being touched.
type KubeconfigWatcher struct {
	files    func() []string
	events   ports.EventPublisher
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger

	// last is the fingerprint at the previous tick, and the empty string
	// before the first one — which is why the first tick never publishes.
	last string
	stop chan struct{}
	done chan struct{}
}

// KubeconfigWatcherDeps are the collaborators the watcher needs.
type KubeconfigWatcherDeps struct {
	// Files reports the kubeconfig files being read, in loading order.
	// Required — this is the whole subject of the watch, and it is a function
	// because the list itself changes when a source is added.
	Files func() []string
	// Events receives the change. Required: a watcher nobody hears is a
	// timer.
	Events ports.EventPublisher
	// Interval is how often the files are stat'd. Zero means the default.
	Interval time.Duration
	// Now is the clock, for tests.
	Now func() time.Time
	// Logger receives diagnostics. Optional.
	Logger *slog.Logger
}

// defaultKubeconfigInterval is the tick.
//
// Five seconds because the cost is a handful of stat calls and the benefit is
// that a `kubectl config use-context` in another window is reflected before
// somebody has finished switching back to this one. It is not a value anybody
// should need to tune, which is why it is not a setting.
const defaultKubeconfigInterval = 5 * time.Second

// NewKubeconfigWatcher validates deps and returns the watcher, stopped.
func NewKubeconfigWatcher(deps KubeconfigWatcherDeps) (*KubeconfigWatcher, error) {
	switch {
	case deps.Files == nil:
		return nil, fmt.Errorf("application: KubeconfigWatcher requires Files")
	case deps.Events == nil:
		return nil, fmt.Errorf("application: KubeconfigWatcher requires Events")
	}

	interval := deps.Interval
	if interval <= 0 {
		interval = defaultKubeconfigInterval
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &KubeconfigWatcher{
		files:    deps.Files,
		events:   deps.Events,
		interval: interval,
		now:      now,
		logger:   logger.With(slog.String("service", "kubeconfig-watch")),
	}, nil
}

// Start begins watching until Stop is called.
//
// The FIRST fingerprint is taken without publishing: at start-up the files are
// simply what they are, and an event on launch would tell the interface its
// freshly read list is stale.
func (w *KubeconfigWatcher) Start(ctx context.Context) {
	if w.stop != nil {
		return
	}
	w.stop = make(chan struct{})
	w.done = make(chan struct{})
	w.last = w.fingerprint()

	go func() {
		defer close(w.done)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-ticker.C:
				w.check(ctx)
			}
		}
	}()
}

// Stop ends the watch and waits for its goroutine, so a caller that stops it
// during shutdown knows nothing is still stat'ing files.
func (w *KubeconfigWatcher) Stop() {
	if w.stop == nil {
		return
	}
	close(w.stop)
	<-w.done
	w.stop = nil
	w.done = nil
}

// check compares the fingerprint and publishes when it moved.
func (w *KubeconfigWatcher) check(ctx context.Context) {
	current := w.fingerprint()
	if current == w.last {
		return
	}
	w.last = current

	files := len(w.files())
	w.logger.DebugContext(ctx, "kubeconfig changed on disk", slog.Int("files", files))
	w.events.Publish(ctx, domain.KubeconfigChanged{Files: files, At: w.now()})
}

// fingerprint is what the files look like from the outside.
//
// PATH, SIZE AND MODIFICATION TIME, and nothing opened. A hash of the contents
// would catch the one case this misses — a write that changes bytes without
// changing size or mtime, which needs a deliberate touch to arrange — at the
// cost of reading every kubeconfig on every tick, including the large merged
// ones some organisations hand out. The trade is deliberate: this is a signal
// to re-read, not an audit.
//
// A FILE THAT CANNOT BE STAT'D IS PART OF THE FINGERPRINT TOO, recorded as
// missing rather than skipped. Otherwise deleting a file and restoring it
// would both be invisible, which is exactly the case somebody hits when a sync
// client replaces a folder.
func (w *KubeconfigWatcher) fingerprint() string {
	paths := w.files()
	// Sorted, because the loading order can change for reasons that are not a
	// change to the files themselves — and a reorder IS worth an event, but it
	// is one the source list already reports through its own call.
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	var builder strings.Builder
	for _, path := range sorted {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&builder, "%s\x00missing\n", path)
			continue
		}
		fmt.Fprintf(&builder, "%s\x00%d\x00%d\n", path, info.Size(), info.ModTime().UnixNano())
	}
	return builder.String()
}
