package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

func TestTerminationDiagnosisReadsTheReasonNotOnlyTheCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		termination domain.Termination
		wantSaid    string
		wantNotSaid string
		wantAlarm   bool
	}{
		{
			// The case everybody knows, and the only one where "137 means
			// out of memory" is actually true.
			name:        "137 with the kubelet calling it OOMKilled",
			termination: domain.Termination{ExitCode: 137, Signal: 9, Reason: "OOMKilled"},
			wantSaid:    "memory limit",
			wantAlarm:   true,
		},
		{
			// THE CASE EVERY ARTICLE GETS WRONG. Same exit code, no OOM
			// reason: something outside the container killed it, and telling
			// somebody to raise a memory limit sends them to tune a number
			// that had nothing to do with it.
			name:        "137 without an OOM reason is not a memory problem",
			termination: domain.Termination{ExitCode: 137, Signal: 9, Reason: "Error"},
			wantSaid:    "grace period",
			// It may MENTION the memory limit — it says the limit was not the
			// cause — but it must never send somebody off to raise one.
			wantNotSaid: "Raise the limit",
			wantAlarm:   true,
		},
		{
			// A rolling update stops every container this way. Colouring it
			// is how a panel teaches people to ignore it.
			name:        "143 is how a rollout stops things",
			termination: domain.Termination{ExitCode: 143, Signal: 15, Reason: "Completed"},
			wantSaid:    "normal path",
			wantAlarm:   false,
		},
		{
			name:        "a clean exit is still a stop",
			termination: domain.Termination{ExitCode: 0, Reason: "Completed"},
			wantSaid:    "exited cleanly",
			wantAlarm:   false,
		},
		{
			name:        "any other status points at the logs",
			termination: domain.Termination{ExitCode: 1, Reason: "Error"},
			wantSaid:    "status 1",
			wantAlarm:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := test.termination.Diagnosis()
			if !strings.Contains(got, test.wantSaid) {
				t.Errorf("Diagnosis() = %q, want it to mention %q", got, test.wantSaid)
			}
			if test.wantNotSaid != "" && strings.Contains(got, test.wantNotSaid) {
				t.Errorf("Diagnosis() = %q, must NOT mention %q", got, test.wantNotSaid)
			}
			if alarm := test.termination.Alarming(); alarm != test.wantAlarm {
				t.Errorf("Alarming() = %v, want %v", alarm, test.wantAlarm)
			}
		})
	}
}

func TestTerminationZeroValueSaysNothing(t *testing.T) {
	t.Parallel()

	var none domain.Termination
	if !none.IsZero() {
		t.Error("the zero value must report itself as absent")
	}
	if none.Diagnosis() != "" {
		t.Errorf("Diagnosis() = %q, want empty: there was no previous life to explain", none.Diagnosis())
	}
	if none.Alarming() {
		t.Error("nothing to be alarmed about when nothing terminated")
	}
}

func TestTerminationLifetimeSeparatesAWeekFromThreeSeconds(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)

	// The number that distinguishes a container that ran for days from one
	// that died on startup — the same restart count, entirely different faults.
	lived := domain.Termination{
		ExitCode: 1, Reason: "Error",
		StartedAt: start, FinishedAt: start.Add(72 * time.Hour),
	}
	if got := lived.Lifetime(); got != 72*time.Hour {
		t.Errorf("Lifetime() = %v, want 72h", got)
	}

	// Timestamps are not guaranteed: a partial record must not produce a
	// duration measured from the zero date.
	partial := domain.Termination{ExitCode: 1, Reason: "Error", FinishedAt: start}
	if got := partial.Lifetime(); got != 0 {
		t.Errorf("Lifetime() with no start = %v, want 0", got)
	}
}
