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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

const (
	// readChunk is how much of a file is read per step when scanning back
	// from the end. Comfortably more than one screenful of samples, so the
	// common windows finish in a single read.
	readChunk = 64 << 10

	// maxLine bounds the partial line carried between chunks, so a corrupt
	// file with no newline in it cannot be accumulated whole in memory.
	maxLine = 64 << 10
)

// readFile reads the samples at or after cutoff, oldest first.
//
// It scans BACKWARDS from the end of the file and stops once it is past the
// cutoff, which makes the cost proportional to the window asked for rather
// than to the retention setting. The chart never wants more than a day, while
// a file may hold ninety of them: reading all of it to answer "the last hour"
// made a 26 MB file cost a third of a second on every refresh, and a 400 KB
// one cost the same five milliseconds it costs now.
//
// This relies on the file being in time order, which appending guarantees. A
// clock stepping backwards — NTP correcting, a laptop resuming — can break
// that locally, so the scan finishes the chunk it is in before stopping rather
// than halting at the first sample it sees outside the window. That absorbs
// disorder of a few hundred samples; the worst a larger jump can do is end a
// chart early, never corrupt what is stored.
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

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("history: reading samples: %w", err)
	}

	samples := make(domain.Series, 0, 512)
	// carry holds the bytes of the line straddling the start of the chunk
	// just read, which belong to the chunk before it.
	var carry []byte
	offset := info.Size()

	for offset > 0 {
		start := max(offset-readChunk, 0)

		buffer := make([]byte, offset-start, offset-start+int64(len(carry)))
		if _, err := file.ReadAt(buffer, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("history: reading samples: %w", err)
		}
		buffer = append(buffer, carry...)
		offset = start

		lines := bytes.Split(buffer, []byte{'\n'})
		// Everything before the first newline is only part of a line unless
		// this chunk reached the start of the file.
		carry = nil
		if start > 0 {
			if len(lines[0]) <= maxLine {
				carry = lines[0]
			}
			lines = lines[1:]
		}

		// Newest first within the chunk, so `passed` means "older than the
		// window" rather than "not there yet".
		passed := false
		for i := len(lines) - 1; i >= 0; i-- {
			var wire wireSample
			if err := json.Unmarshal(lines[i], &wire); err != nil || wire.T == 0 {
				continue
			}
			sample := wire.toDomain()
			if sample.At.Before(cutoff) {
				passed = true
				continue
			}
			samples = append(samples, sample)
		}
		if passed {
			break
		}
	}

	slices.Reverse(samples)
	return samples, nil
}

// Prune discards samples older than cutoff across every cluster.
//
// Rewrites each file through a temporary and renames it into place, so a
// crash mid-prune leaves the previous file intact rather than a half-written
// one. A file that ends up empty is removed entirely — a cluster nobody has
// opened in a month should leave nothing behind.
//
// The lock is taken and released per file rather than held across the sweep.
// Somebody with fifty clusters in their kubeconfig has fifty files here, and
// holding it throughout meant the hourly prune stopped both sampling and every
// chart read for as long as the whole rewrite took — seconds, at long
// retentions. A reader now waits for one file at most.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) error {
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
		if err := s.pruneOne(filepath.Join(s.dir, entry.Name()), cutoff); err != nil {
			return err
		}
	}
	return nil
}

// pruneOne enforces the retention window on one file.
//
// A file whose last write predates the cutoff is discarded without being read.
// Samples are appended as they are taken, so a file's modification time is its
// newest sample: if that has expired, so has everything before it. This is
// what keeps a kubeconfig full of clusters cheap — the ones nobody has opened
// for weeks cost a stat and an unlink each rather than a full rewrite.
func (s *Store) pruneOne(path string, cutoff time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("history: inspecting a sample file: %w", err)
	case info.ModTime().Before(cutoff):
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("history: removing an expired sample file: %w", err)
		}
		return nil
	}

	return pruneFile(path, cutoff)
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
