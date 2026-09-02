package config_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/config"
)

func TestPodsAreWatchedUnlessSomebodySaysOtherwise(t *testing.T) {
	// The default changed, so the switch is now the ONLY way back to
	// re-listing. A switch that silently does nothing is worse than no
	// switch: it is a documented escape hatch that is not there.
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.Kubernetes.LiveWatch {
		t.Fatal("pods are not watched by default")
	}

	t.Setenv("PODSTEER_LIVE_WATCH", "false")
	off, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if off.Kubernetes.LiveWatch {
		t.Fatal("PODSTEER_LIVE_WATCH=false did not turn the watch off")
	}

	t.Setenv("PODSTEER_LIVE_WATCH", "1")
	on, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !on.Kubernetes.LiveWatch {
		t.Fatal("PODSTEER_LIVE_WATCH=1 did not turn the watch on")
	}
}

func TestAMisspeltSwitchIsAnErrorRatherThanASilentDefault(t *testing.T) {
	// The same rule the rest of this file follows: somebody who writes
	// PODSTEER_LIVE_WATCH=no needs to be told, not quietly given a watch they
	// were trying to turn off.
	t.Setenv("PODSTEER_LIVE_WATCH", "no thanks")

	if _, err := config.Load(); err == nil {
		t.Fatal("a malformed switch was accepted")
	}
}
