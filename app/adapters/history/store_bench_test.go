package history_test

// Benchmarks for the two operations whose cost grows with the retention
// setting rather than with anything the operator can see.
//
// The sizes are the configurable range, not round numbers: 2,880 samples is a
// day at the default cadence, and 777,600 is the ceiling the settings allow
// (90 days at 10 seconds, about 105 MB). Series must stay flat across all of
// them — it reads the window asked for, not the file — and the day it stops
// being flat is the day something started reading the whole file again.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/adapters/history"
	"github.com/podsteer/podsteer/app/domain"
)

// seedSamples writes n samples one interval apart, ending now.
func seedSamples(tb testing.TB, dir string, n int, interval time.Duration) {
	tb.Helper()

	store := history.New(dir)
	base := time.Now().UTC().Add(-time.Duration(n) * interval)
	for i := range n {
		sample := domain.Sample{
			At:                  base.Add(time.Duration(i) * interval),
			CPUUsageMilli:       3040,
			CPURequestsMilli:    47360,
			CPUAllocatableMilli: 102000,
			MemoryUsageBytes:    125_000_000_000,
			MemoryRequestsBytes: 123_000_000_000,
			MemoryAllocBytes:    206_000_000_000,
			PodsScheduled:       204,
			PodsNotReady:        3,
			NodesReady:          18,
			NodesTotal:          18,
			Measured:            true,
		}
		if err := store.Append(context.Background(), "bench", sample); err != nil {
			tb.Fatalf("seeding: %v", err)
		}
	}
}

// BenchmarkSeriesWindow reads one hour out of files of every supported size.
func BenchmarkSeriesWindow(b *testing.B) {
	for _, samples := range []int{2880, 86400, 777600} {
		b.Run(fmt.Sprint(samples), func(b *testing.B) {
			dir := b.TempDir()
			seedSamples(b, dir, samples, 10*time.Second)
			store := history.New(dir)
			cutoff := time.Now().UTC().Add(-time.Hour)

			b.ResetTimer()
			for b.Loop() {
				if _, err := store.Series(context.Background(), "bench", cutoff); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAppend(b *testing.B) {
	store := history.New(b.TempDir())
	sample := domain.Sample{At: time.Now().UTC(), CPUUsageMilli: 3040, PodsScheduled: 204, Measured: true}

	b.ResetTimer()
	for b.Loop() {
		if err := store.Append(context.Background(), "bench", sample); err != nil {
			b.Fatal(err)
		}
	}
}
