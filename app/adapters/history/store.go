// Package history stores the samples PodSteer takes of a cluster over time.
//
// Local files, in the user's own configuration directory, because that is what
// the feature promises: a record of the window the application was open, kept
// on this machine and nowhere else. There is no telemetry here and no upload —
// the whole point of the retention setting is that an operator can say how
// long their cluster's capacity profile lives on their own disk, including
// "not at all".
//
// The format is JSON Lines: one sample per line, appended. It is trivially
// durable under a crash — a torn final line is discarded on read and nothing
// else is affected — and it needs no database. A file per cluster keeps the
// pruning of one from touching another.
package history

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// Store is a file-backed sample store.
//
// Safe for concurrent use: the sampler writes from its own goroutine while the
// UI reads from Wails call handlers, and both can arrive at once.
type Store struct {
	dir string
	mu  sync.RWMutex
}

var _ ports.HistoryPort = (*Store)(nil)

// New returns a store writing beneath dir, creating it on first write.
func New(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultDir returns the per-user directory PodSteer records into.
func DefaultDir() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("history: locating the user config directory: %w", err)
	}
	return filepath.Join(config, "PodSteer", "history"), nil
}

// wireSample is the on-disk shape.
//
// Short keys and Unix seconds rather than the domain's field names and RFC
// 3339: this is written every sampling interval for every open cluster and
// kept for days, and the difference is roughly half the file. It is a private
// format, so nothing outside this package depends on the abbreviations.
type wireSample struct {
	T  int64 `json:"t"`
	CU int64 `json:"cu,omitempty"`
	CR int64 `json:"cr,omitempty"`
	CA int64 `json:"ca,omitempty"`
	MU int64 `json:"mu,omitempty"`
	MR int64 `json:"mr,omitempty"`
	MA int64 `json:"ma,omitempty"`
	P  int   `json:"p,omitempty"`
	PN int   `json:"pn,omitempty"`
	NR int   `json:"nr,omitempty"`
	NT int   `json:"nt,omitempty"`
	M  bool  `json:"m,omitempty"`
}

func toWire(sample domain.Sample) wireSample {
	return wireSample{
		T:  sample.At.UTC().Unix(),
		CU: sample.CPUUsageMilli,
		CR: sample.CPURequestsMilli,
		CA: sample.CPUAllocatableMilli,
		MU: sample.MemoryUsageBytes,
		MR: sample.MemoryRequestsBytes,
		MA: sample.MemoryAllocBytes,
		P:  sample.PodsScheduled,
		PN: sample.PodsNotReady,
		NR: sample.NodesReady,
		NT: sample.NodesTotal,
		M:  sample.Measured,
	}
}

func (w wireSample) toDomain() domain.Sample {
	return domain.Sample{
		At:                  time.Unix(w.T, 0).UTC(),
		CPUUsageMilli:       w.CU,
		CPURequestsMilli:    w.CR,
		CPUAllocatableMilli: w.CA,
		MemoryUsageBytes:    w.MU,
		MemoryRequestsBytes: w.MR,
		MemoryAllocBytes:    w.MA,
		PodsScheduled:       w.P,
		PodsNotReady:        w.PN,
		NodesReady:          w.NR,
		NodesTotal:          w.NT,
		Measured:            w.M,
	}
}

// unsafeForFilename matches everything a kubeconfig context name may contain
// that a path segment must not.
var unsafeForFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// fileFor returns the path a cluster's samples live in.
//
// Kubeconfig context names are arbitrary strings — ARNs full of slashes and
// colons are routine on EKS — so the readable part is sanitised for display
// and a hash of the original is appended for identity. Sanitising alone would
// collide two clusters whose names differ only in punctuation, and hashing
// alone would leave a directory nobody can read.
func (s *Store) fileFor(id domain.ClusterID) string {
	raw := string(id)
	digest := sha256.Sum256([]byte(raw))

	readable := unsafeForFilename.ReplaceAllString(raw, "-")
	readable = strings.Trim(readable, "-")
	if len(readable) > 48 {
		readable = readable[:48]
	}
	if readable == "" {
		readable = "cluster"
	}

	return filepath.Join(s.dir, fmt.Sprintf("%s.%s.jsonl", readable, hex.EncodeToString(digest[:4])))
}

// Append records one sample.
func (s *Store) Append(_ context.Context, id domain.ClusterID, sample domain.Sample) error {
	if sample.IsZero() {
		return nil
	}

	line, err := json.Marshal(toWire(sample))
	if err != nil {
		return fmt.Errorf("history: encoding sample: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("history: creating %s: %w", s.dir, err)
	}

	// 0600 throughout: this is a record of the operator's own infrastructure
	// and no other user of the machine has business reading it.
	file, err := os.OpenFile(s.fileFor(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("history: opening the sample file: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("history: writing sample: %w", err)
	}
	return nil
}

// Series returns a cluster's samples at or after cutoff, oldest first.
func (s *Store) Series(_ context.Context, id domain.ClusterID, cutoff time.Time) (domain.Series, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	samples, err := readFile(s.fileFor(id), cutoff)
	if err != nil {
		return nil, err
	}
	samples.Sort()
	return samples, nil
}

// readFile reads one sample file, skipping anything unparseable.
//
// A malformed line is skipped rather than failing the read: the last line of
// an append-only file can be torn by a crash or a full disk, and losing one
// sample must never cost the operator the other twenty thousand.
func readFile(path string, cutoff time.Time) (domain.Series, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("history: reading samples: %w", err)
	}
	defer func() { _ = file.Close() }()

	samples := make(domain.Series, 0, 512)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)

	for scanner.Scan() {
		var wire wireSample
		if err := json.Unmarshal(scanner.Bytes(), &wire); err != nil || wire.T == 0 {
			continue
		}
		sample := wire.toDomain()
		if sample.At.Before(cutoff) {
			continue
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("history: reading samples: %w", err)
	}
	return samples, nil
}

// Prune discards samples older than cutoff across every cluster.
//
// Rewrites each file through a temporary and renames it into place, so a
// crash mid-prune leaves the previous file intact rather than a half-written
// one. A file that ends up empty is removed entirely — a cluster nobody has
// opened in a month should leave nothing behind.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("history: listing the sample directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := pruneFile(filepath.Join(s.dir, entry.Name()), cutoff); err != nil {
			return err
		}
	}
	return nil
}

func pruneFile(path string, cutoff time.Time) error {
	samples, err := readFile(path, cutoff)
	if err != nil {
		return err
	}

	if len(samples) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("history: removing an empty sample file: %w", err)
		}
		return nil
	}

	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("history: rewriting samples: %w", err)
	}

	writer := bufio.NewWriter(file)
	for _, sample := range samples {
		line, err := json.Marshal(toWire(sample))
		if err != nil {
			continue
		}
		if _, err := writer.Write(append(line, '\n')); err != nil {
			_ = file.Close()
			_ = os.Remove(temporary)
			return fmt.Errorf("history: rewriting samples: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		_ = os.Remove(temporary)
		return fmt.Errorf("history: flushing samples: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("history: closing the sample file: %w", err)
	}

	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("history: replacing the sample file: %w", err)
	}
	return nil
}

// Forget discards everything recorded for one cluster.
func (s *Store) Forget(_ context.Context, id domain.ClusterID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.fileFor(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("history: forgetting a cluster's samples: %w", err)
	}
	return nil
}

// Clear removes every sample file, for when recording is turned off.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("history: listing the sample directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, entry.Name())); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("history: clearing samples: %w", err)
		}
	}
	return nil
}
