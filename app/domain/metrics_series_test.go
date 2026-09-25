package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func backendFixture() domain.MetricsBackend {
	return domain.MetricsBackend{
		Kind:      domain.MetricsBackendPrometheus,
		Namespace: "monitoring",
		Service:   "prometheus-operated",
		Port:      "web",
	}
}

// THE CHECK THAT DECIDES WHETHER A NUMBER IS ABOUT THIS CLUSTER. Subset, not
// overlap: a Thanos in front of six clusters overlaps with each of them and
// holds the other five besides, and an overlap test draws all six under one
// cluster's name.
func TestVerifyBackendNodesHasFourOutcomes(t *testing.T) {
	ours := []string{"node-a", "node-b", "node-c"}

	cases := []struct {
		name    string
		backend []string
		cluster []string
		want    domain.BackendVerification
	}{
		{
			name:    "exactly this cluster",
			backend: []string{"node-a", "node-b", "node-c"},
			cluster: ours,
			want:    domain.VerificationVerified,
		},
		{
			name:    "a subset of this cluster, which a quiet node produces",
			backend: []string{"node-a"},
			cluster: ours,
			want:    domain.VerificationVerified,
		},
		{
			name:    "this cluster and strangers besides, which is Thanos",
			backend: []string{"node-a", "node-b", "other-1", "other-2"},
			cluster: ours,
			want:    domain.VerificationFleet,
		},
		{
			name:    "a superset with every one of ours in it",
			backend: []string{"node-a", "node-b", "node-c", "other-1"},
			cluster: ours,
			want:    domain.VerificationFleet,
		},
		{
			name:    "disjoint",
			backend: []string{"other-1", "other-2"},
			cluster: ours,
			want:    domain.VerificationMismatch,
		},
		{
			name:    "the backend answered with nothing",
			backend: nil,
			cluster: ours,
			want:    domain.VerificationUnverifiable,
		},
		{
			name:    "PodSteer could not list this cluster's nodes",
			backend: []string{"node-a"},
			cluster: nil,
			want:    domain.VerificationUnverifiable,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := domain.VerifyBackendNodes(test.backend, test.cluster); got != test.want {
				t.Fatalf("verification %q, want %q", got, test.want)
			}
		})
	}
}

// An overlap test would pass the fleet case, and that is the whole reason
// this function exists — so it is asserted directly rather than left implied
// by the table above.
func TestAnOverlappingFleetIsNotVerified(t *testing.T) {
	got := domain.VerifyBackendNodes(
		[]string{"node-a", "prod-1", "prod-2", "prod-3"},
		[]string{"node-a", "node-b"},
	)
	if got == domain.VerificationVerified {
		t.Fatal("a backend holding four other clusters' nodes was verified")
	}
	if got != domain.VerificationFleet {
		t.Fatalf("verification %q, want fleet", got)
	}
}

// A Prometheus that scrapes application endpoints and not kubelets answers
// 200 with nothing. Collapsed into "answered" that is a blank chart under a
// green label.
func TestAnEmptyAnswerIsItsOwnStatus(t *testing.T) {
	backend := backendFixture()

	empty := domain.AnsweredResult(backend, domain.VerificationVerified, false, "sum(x)", time.Minute, nil)
	if empty.Status != domain.BackendAnsweredEmpty {
		t.Fatalf("status %q, want answered-empty", empty.Status)
	}
	if empty.Status.Drawable() {
		t.Fatal("an empty answer reported itself as drawable")
	}
	if empty.Message == "" {
		t.Fatal("an empty answer says nothing about why the chart is blank")
	}

	// A series with a label set and no points is the same fact by another
	// route, and reads as answered unless it is checked for.
	shaped := domain.AnsweredResult(backend, domain.VerificationVerified, false, "sum(x)", time.Minute,
		[]domain.PromSeries{{Labels: map[string]string{"node": "node-a"}}})
	if shaped.Status != domain.BackendAnsweredEmpty {
		t.Fatalf("status %q for a pointless series, want answered-empty", shaped.Status)
	}
}

func TestAnAnswerWithPointsIsDrawable(t *testing.T) {
	now := time.Now()
	result := domain.AnsweredResult(backendFixture(), domain.VerificationVerified, false, "sum(x)", time.Minute,
		[]domain.PromSeries{{Points: []domain.SeriesPoint{
			{At: now.Add(-time.Hour), Value: 1},
			{At: now, Value: 2},
		}}})

	if result.Status != domain.BackendAnswered {
		t.Fatalf("status %q, want answered", result.Status)
	}
	if !result.Status.Drawable() {
		t.Fatal("an answer with points is not drawable")
	}
	if span := result.Span(); span < 59*time.Minute || span > 61*time.Minute {
		t.Fatalf("span %s, want about an hour", span)
	}
}

// Every series carries its provenance, and a backend's is never PodSteer's.
func TestEverySeriesSaysWhoseMeasurementItIs(t *testing.T) {
	backend := backendFixture()
	result := domain.AnsweredResult(backend, domain.VerificationFleet, true, "sum(x)", time.Minute,
		[]domain.PromSeries{{Points: []domain.SeriesPoint{{At: time.Now(), Value: 1}}}})

	if result.Provenance.Origin != domain.OriginBackend {
		t.Fatalf("origin %q, want backend", result.Provenance.Origin)
	}
	if result.Provenance.Source != backend.Describe() {
		t.Fatalf("source %q, want %q", result.Provenance.Source, backend.Describe())
	}
	if result.Provenance.Verification != domain.VerificationFleet {
		t.Fatalf("verification %q was not carried", result.Provenance.Verification)
	}
	if !result.Provenance.Filtered {
		t.Fatal("a narrowed sum did not say it was narrowed")
	}

	if sampled := domain.SampledProvenance(); sampled.Origin != domain.OriginSampled || sampled.Source != "" {
		t.Fatalf("the sampled provenance names a source: %+v", sampled)
	}
}

// A refusal that explains itself, in words that differ by outcome — an
// operator told "no data" cannot tell a fleet backend from a broken feature.
func TestARefusedAggregateSaysWhichCaseItWas(t *testing.T) {
	backend := backendFixture()

	for _, verification := range []domain.BackendVerification{
		domain.VerificationFleet,
		domain.VerificationMismatch,
		domain.VerificationUnverifiable,
	} {
		result := domain.UnverifiedResult(backend, verification)

		if result.Status != domain.BackendUnverified {
			t.Fatalf("%s: status %q, want unverified", verification, result.Status)
		}
		if result.Status.Drawable() {
			t.Fatalf("%s: an unverified backend reported itself drawable", verification)
		}
		if !strings.Contains(result.Message, backend.Describe()) {
			t.Fatalf("%s: the refusal does not name the backend: %s", verification, result.Message)
		}
		if len(result.Series) != 0 {
			t.Fatalf("%s: an unverified result carries series", verification)
		}
	}

	fleet := domain.UnverifiedResult(backend, domain.VerificationFleet).Message
	mismatch := domain.UnverifiedResult(backend, domain.VerificationMismatch).Message
	if fleet == mismatch {
		t.Fatal("a fleet backend and a mismatched one say the same thing")
	}
}

func TestNotEnabledSaysWhereToTurnItOn(t *testing.T) {
	result := domain.NotEnabled()
	if result.Status != domain.BackendNotEnabled {
		t.Fatalf("status %q, want not-enabled", result.Status)
	}
	if !strings.Contains(result.Message, "Settings") {
		t.Fatalf("the message does not say where the switch is: %s", result.Message)
	}
	if len(result.Series) != 0 || result.Expression != "" {
		t.Fatal("a cluster nobody enabled carries an expression or a series")
	}
}
