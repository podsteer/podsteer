package domain_test

import (
	"testing"
	"time"

	"podsteer/app/domain"
)

func TestRetentionClampsToSupportedRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		days int
		want int
	}{
		{name: "zero means record nothing", days: 0, want: 0},
		{name: "negative is not a window", days: -5, want: 0},
		{name: "ordinary value survives", days: 7, want: 7},
		{name: "beyond the ceiling is capped", days: 5000, want: domain.MaxRetentionDays},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			retention := domain.NewRetention(test.days)
			if retention.Days != test.want {
				t.Errorf("days = %d, want %d", retention.Days, test.want)
			}
			if retention.Enabled() != (test.want > 0) {
				t.Errorf("enabled = %v, want %v", retention.Enabled(), test.want > 0)
			}
		})
	}
}

// Turning recording off must not silently mean "keep everything forever with
// a cutoff in the past".
func TestDisabledRetentionCutsOffAtNow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	if got := domain.NewRetention(0).Cutoff(now); !got.Equal(now) {
		t.Errorf("cutoff = %v, want now — a zero window discards everything", got)
	}
	if got := domain.NewRetention(7).Cutoff(now); !got.Equal(now.AddDate(0, 0, -7)) {
		t.Errorf("cutoff = %v, want seven days back", got)
	}
}

// A series shorter than the limit must be returned untouched; averaging a
// two-point series into two points should not perturb it.
func TestDownsampleLeavesShortSeriesAlone(t *testing.T) {
	t.Parallel()

	series := makeSeries(t, 10, 100)
	got := series.Downsample(50)

	if len(got) != 10 {
		t.Fatalf("length = %d, want the original 10", len(got))
	}
	if got[0].CPUUsageMilli != series[0].CPUUsageMilli {
		t.Errorf("first sample changed: %d != %d", got[0].CPUUsageMilli, series[0].CPUUsageMilli)
	}
}

func TestDownsampleReducesToTheLimit(t *testing.T) {
	t.Parallel()

	series := makeSeries(t, 2000, 100)
	got := series.Downsample(120)

	if len(got) > 120 {
		t.Fatalf("length = %d, want at most 120", len(got))
	}
	// The series must still end where it ended: a chart whose last point is
	// an average of the last two minutes, timestamped two minutes ago, looks
	// like the cluster stopped reporting.
	if !got[len(got)-1].At.Equal(series[len(series)-1].At) {
		t.Errorf("last timestamp = %v, want the series' own end %v",
			got[len(got)-1].At, series[len(series)-1].At)
	}
}

// Averaging rather than dropping is the whole reason a spike survives.
func TestDownsampleKeepsSpikesVisible(t *testing.T) {
	t.Parallel()

	series := makeSeries(t, 100, 100)
	series[50].CPUUsageMilli = 10_000

	got := series.Downsample(10)

	var peak int64
	for _, sample := range got {
		if sample.CPUUsageMilli > peak {
			peak = sample.CPUUsageMilli
		}
	}
	if peak <= 100 {
		t.Errorf("peak = %dm, want the spike to survive as a raised average", peak)
	}
}

// A gap in metrics-server must not drag the usage line towards zero: the
// unmeasured samples carry no usage to average in.
func TestDownsampleIgnoresUnmeasuredSamplesWhenAveragingUsage(t *testing.T) {
	t.Parallel()

	series := make(domain.Series, 0, 4)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for i := range 4 {
		sample := domain.Sample{
			At:               base.Add(time.Duration(i) * time.Minute),
			CPURequestsMilli: 1000,
			Measured:         i < 2,
		}
		if sample.Measured {
			sample.CPUUsageMilli = 500
		}
		series = append(series, sample)
	}

	got := series.Downsample(1)
	if len(got) != 1 {
		t.Fatalf("length = %d, want 1", len(got))
	}
	if got[0].CPUUsageMilli != 500 {
		t.Errorf("usage = %dm, want 500m — the two unmeasured samples must not average in",
			got[0].CPUUsageMilli)
	}
	if !got[0].Measured {
		t.Error("the bucket held measurements and must say so")
	}
}

// Nothing measured at all must stay unmeasured, so the chart leaves a gap
// rather than drawing a confident zero.
func TestDownsampleReportsEntirelyUnmeasuredBuckets(t *testing.T) {
	t.Parallel()

	series := make(domain.Series, 0, 4)
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	for i := range 4 {
		series = append(series, domain.Sample{
			At:               base.Add(time.Duration(i) * time.Minute),
			CPURequestsMilli: 1000,
		})
	}

	got := series.Downsample(1)
	if got[0].Measured {
		t.Error("no sample was measured, so the average must not claim to be")
	}
}

func TestSeriesSpanReportsWhatIsActuallyHeld(t *testing.T) {
	t.Parallel()

	if got := (domain.Series{}).Span(); got != 0 {
		t.Errorf("empty span = %v, want 0", got)
	}
	if got := makeSeries(t, 1, 100).Span(); got != 0 {
		t.Errorf("single-sample span = %v, want 0 — one point is not a period", got)
	}

	series := makeSeries(t, 61, 100)
	if got := series.Span(); got != time.Hour {
		t.Errorf("span = %v, want 1h", got)
	}
}

func TestSampleFromOverviewUsesPodUsageNotNodeUsage(t *testing.T) {
	t.Parallel()

	overview := domain.Overview{
		GeneratedAt: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
		Capacity: domain.CapacitySummary{
			// Usage is measured across nodes and includes the kubelet and the
			// OS; PodUsage is what the pods themselves consume. The chart
			// plots usage against requests, so it has to use the pod figure
			// or the two lines are not comparable.
			CPU: domain.ResourceUsage{
				Allocatable: 4000, Requests: 2000, Usage: 900, PodUsage: 500, Measured: true,
			},
			Memory: domain.ResourceUsage{Allocatable: 8 << 30, Requests: 4 << 30, PodUsage: 1 << 30, Measured: true},
			Pods:   domain.PodCapacity{Scheduled: 12},
		},
		Pods:  domain.PodSummary{NotReady: 2},
		Nodes: domain.NodeSummary{Ready: 3, Total: 3},
	}

	sample := domain.NewSampleFromOverview(overview)

	if sample.CPUUsageMilli != 500 {
		t.Errorf("cpu usage = %dm, want the pod figure 500m", sample.CPUUsageMilli)
	}
	if sample.PodsScheduled != 12 || sample.PodsNotReady != 2 {
		t.Errorf("pods = %d scheduled / %d not ready, want 12/2", sample.PodsScheduled, sample.PodsNotReady)
	}
	if !sample.Measured {
		t.Error("the sample must carry that it was measured")
	}
}

// makeSeries builds count samples one minute apart.
func makeSeries(t *testing.T, count int, cpu int64) domain.Series {
	t.Helper()

	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	series := make(domain.Series, 0, count)
	for i := range count {
		series = append(series, domain.Sample{
			At:                  base.Add(time.Duration(i) * time.Minute),
			CPUUsageMilli:       cpu,
			CPURequestsMilli:    cpu * 4,
			CPUAllocatableMilli: cpu * 10,
			PodsScheduled:       10,
			NodesReady:          3,
			NodesTotal:          3,
			Measured:            true,
		})
	}
	return series
}
