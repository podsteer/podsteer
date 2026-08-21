package history_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/adapters/history"
	"github.com/podsteer/podsteer/app/domain"
)

func sample(at time.Time, cpu int64) domain.Sample {
	return domain.Sample{
		At:               at.UTC(),
		CPUUsageMilli:    cpu,
		CPURequestsMilli: cpu * 2,
		PodsScheduled:    10,
		Measured:         true,
	}
}

func TestAppendAndReadBack(t *testing.T) {
	t.Parallel()

	store := history.New(t.TempDir())
	ctx := context.Background()
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	for i := range 5 {
		if err := store.Append(ctx, "dev", sample(base.Add(time.Duration(i)*time.Minute), int64(100+i))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	series, err := store.Series(ctx, "dev", base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series) != 5 {
		t.Fatalf("length = %d, want 5", len(series))
	}
	if series[0].CPUUsageMilli != 100 || series[4].CPUUsageMilli != 104 {
		t.Errorf("series = %v, want the samples in the order they were written", series)
	}
	if !series[0].Measured {
		t.Error("measured must survive the round trip")
	}
}

// Two clusters must not share a file, or pruning one would rewrite the other.
func TestClustersAreStoredSeparately(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "dev", sample(now, 100)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(ctx, "prod", sample(now, 900)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	dev, err := store.Series(ctx, "dev", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(dev) != 1 || dev[0].CPUUsageMilli != 100 {
		t.Errorf("dev series = %v, want only its own sample", dev)
	}

	files, _ := os.ReadDir(dir)
	if len(files) != 2 {
		t.Errorf("files = %d, want one per cluster", len(files))
	}
}

// Context names are arbitrary strings — EKS ARNs are full of slashes and
// colons — and none of it may become a path.
func TestAwkwardClusterNamesStayInsideTheDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	awkward := domain.ClusterID("arn:aws:eks:eu-central-1:123456789012:cluster/../../escape")
	if err := store.Append(ctx, awkward, sample(now, 100)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the store directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("files = %d, want the sample written inside the store directory", len(entries))
	}
	if entries[0].IsDir() {
		t.Fatalf("the cluster name created a directory: %q", entries[0].Name())
	}

	// And it must still be readable under the same id.
	series, err := store.Series(ctx, awkward, now.Add(-time.Hour))
	if err != nil || len(series) != 1 {
		t.Errorf("Series() = %v, %v; want the sample back", series, err)
	}
}

// Two ids that sanitise to the same readable name must not collide.
func TestSimilarClusterNamesDoNotCollide(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "prod/eu", sample(now, 100)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(ctx, "prod:eu", sample(now, 900)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	first, _ := store.Series(ctx, "prod/eu", now.Add(-time.Hour))
	if len(first) != 1 || first[0].CPUUsageMilli != 100 {
		t.Errorf("series = %v, want only its own sample — the names must not share a file", first)
	}
}

func TestPruneDiscardsOnlyExpiredSamples(t *testing.T) {
	t.Parallel()

	store := history.New(t.TempDir())
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "dev", sample(now.Add(-48*time.Hour), 1)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(ctx, "dev", sample(now.Add(-time.Minute), 2)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	if err := store.Prune(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	series, err := store.Series(ctx, "dev", time.Time{})
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series) != 1 || series[0].CPUUsageMilli != 2 {
		t.Errorf("series = %v, want only the recent sample", series)
	}
}

// Pruning everything must leave nothing behind: an operator who turns
// recording off should not find a month of their capacity profile on disk.
func TestPruneRemovesEmptiedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "dev", sample(now.Add(-time.Hour), 1)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Prune(ctx, now); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the store directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("files = %d, want the emptied file removed", len(entries))
	}
}

// A crash can tear the last line of an append-only file. Losing that sample is
// acceptable; losing the twenty thousand before it is not.
func TestTornLineDoesNotCostTheWholeFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := range 3 {
		if err := store.Append(ctx, "dev", sample(now.Add(time.Duration(i)*time.Minute), int64(i+1))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the sample file: %v", err)
	}
	torn := append(existing, []byte(`{"t":17714,"cu":`)...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatalf("writing a torn line: %v", err)
	}

	series, err := store.Series(ctx, "dev", time.Time{})
	if err != nil {
		t.Fatalf("Series() error = %v, want the readable samples back", err)
	}
	if len(series) != 3 {
		t.Errorf("length = %d, want the three intact samples", len(series))
	}
}

// The chart asks for a window, and the cost should follow the window rather
// than the retention setting.
//
// Seeded well past the 64 KiB chunk the reader steps in, so the backwards scan
// has to stitch lines across a chunk boundary — the case a single-read
// implementation passes by accident.
func TestSeriesReturnsOnlyTheWindowAsked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	// Four days at one sample a minute: ~5,760 lines, roughly 800 KiB.
	const total = 5760
	base := now.Add(-total * time.Minute)
	for i := range total {
		if err := store.Append(ctx, "dev", sample(base.Add(time.Duration(i)*time.Minute), int64(i))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	series, err := store.Series(ctx, "dev", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	// The last hour is 60 samples, give or take the boundary.
	if len(series) < 59 || len(series) > 61 {
		t.Fatalf("length = %d, want about the 60 samples of the last hour", len(series))
	}
	if series[0].At.After(series[len(series)-1].At) {
		t.Error("samples came back newest first, want oldest first")
	}
	if series[0].At.Before(now.Add(-time.Hour).Add(-time.Minute)) {
		t.Errorf("oldest = %v, want nothing from before the cutoff", series[0].At)
	}

	// The whole file is still readable, so nothing was lost by reading less.
	all, err := store.Series(ctx, "dev", time.Time{})
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(all) != total {
		t.Errorf("length = %d, want all %d samples", len(all), total)
	}
	if all[0].CPUUsageMilli != 0 || all[total-1].CPUUsageMilli != total-1 {
		t.Error("the full read is not in the order it was written")
	}
}

// A clock stepping backwards must not truncate a chart at the anomaly.
func TestSeriesToleratesASampleOutOfOrder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := range 30 {
		at := now.Add(-time.Duration(30-i) * time.Minute)
		// One sample lands as though the clock had jumped back a day.
		if i == 15 {
			at = now.Add(-24 * time.Hour)
		}
		if err := store.Append(ctx, "dev", sample(at, int64(i))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	series, err := store.Series(ctx, "dev", now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	// 29 in the window, and the misdated one correctly outside it.
	if len(series) != 29 {
		t.Errorf("length = %d, want the 29 samples either side of the anomaly", len(series))
	}
}

// Fifty clusters in a kubeconfig means fifty files here, most of them
// belonging to clusters nobody has opened in weeks. Those must not be read.
func TestPruneDiscardsADormantFileWithoutRewritingIt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "dormant", sample(now.Add(-72*time.Hour), 1)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	entries, _ := os.ReadDir(dir)
	path := filepath.Join(dir, entries[0].Name())
	// Appending stamps the file with now; date it to when its samples were
	// taken, which is what a cluster left alone for three days looks like.
	stale := now.Add(-72 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("dating the file: %v", err)
	}

	if err := store.Prune(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a file whose newest sample had expired was kept")
	}
}

// The shortcut must not reach a file that is merely quiet.
func TestPruneKeepsAFileWrittenInsideTheWindow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := history.New(dir)
	ctx := context.Background()
	now := time.Now().UTC()

	for i := range 3 {
		if err := store.Append(ctx, "live", sample(now.Add(-time.Duration(i)*time.Minute), int64(i))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	if err := store.Prune(ctx, now.Add(-24*time.Hour)); err != nil {
		t.Fatalf("Prune() error = %v", err)
	}

	series, err := store.Series(ctx, "live", time.Time{})
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series) != 3 {
		t.Errorf("length = %d, want the three current samples kept", len(series))
	}
}

func TestSeriesOfAnUnknownClusterIsEmptyNotAnError(t *testing.T) {
	t.Parallel()

	store := history.New(t.TempDir())

	series, err := store.Series(context.Background(), "never-connected", time.Time{})
	if err != nil {
		t.Fatalf("Series() error = %v, want no error for a cluster with no history", err)
	}
	if len(series) != 0 {
		t.Errorf("series = %v, want empty", series)
	}
}

func TestForgetRemovesOneClustersHistory(t *testing.T) {
	t.Parallel()

	store := history.New(t.TempDir())
	ctx := context.Background()
	now := time.Now().UTC()

	if err := store.Append(ctx, "dev", sample(now, 1)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(ctx, "prod", sample(now, 2)); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Forget(ctx, "dev"); err != nil {
		t.Fatalf("Forget() error = %v", err)
	}

	dev, _ := store.Series(ctx, "dev", time.Time{})
	prod, _ := store.Series(ctx, "prod", time.Time{})
	if len(dev) != 0 {
		t.Errorf("dev series = %v, want it forgotten", dev)
	}
	if len(prod) != 1 {
		t.Errorf("prod series = %v, want it untouched", prod)
	}
}

// Forgetting a cluster that was never recorded is a no-op, not a failure.
func TestForgetIsIdempotent(t *testing.T) {
	t.Parallel()

	store := history.New(t.TempDir())
	if err := store.Forget(context.Background(), "never-connected"); err != nil {
		t.Errorf("Forget() error = %v, want nil", err)
	}
}
