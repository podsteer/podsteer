package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestAHealthyNodeIsNotAWarning(t *testing.T) {
	// THE BUG THIS REPLACED, and it was on screen. One rule — "False is bad"
	// — was applied to every kind, so every healthy node showed its three
	// pressure conditions coloured as warnings, and a node genuinely under
	// MemoryPressure rendered as though nothing were wrong.
	for _, pressure := range []string{"MemoryPressure", "DiskPressure", "PIDPressure", "NetworkUnavailable"} {
		if tone := domain.ClassifyCondition(pressure, "False"); tone != domain.ConditionNormal {
			t.Fatalf("%s=False read as %q; a node with no pressure is the ordinary case", pressure, tone)
		}
		if tone := domain.ClassifyCondition(pressure, "True"); tone != domain.ConditionWarning {
			t.Fatalf("%s=True read as %q; the kubelet is reporting a problem", pressure, tone)
		}
	}

	// And the one node condition that runs the other way.
	if tone := domain.ClassifyCondition("Ready", "False"); tone != domain.ConditionWarning {
		t.Fatalf("Ready=False read as %q", tone)
	}
	if tone := domain.ClassifyCondition("Ready", "True"); tone != domain.ConditionNormal {
		t.Fatalf("Ready=True read as %q", tone)
	}
}

func TestAStuckRolloutIsAWarningAndAProgressingOneIsNot(t *testing.T) {
	// ReplicaFailure carries the quota message that explains a stuck
	// rollout, and nothing else in the panel does.
	if tone := domain.ClassifyCondition("ReplicaFailure", "True"); tone != domain.ConditionWarning {
		t.Fatalf("ReplicaFailure=True read as %q", tone)
	}
	if tone := domain.ClassifyCondition("Progressing", "True"); tone != domain.ConditionNormal {
		t.Fatal("a rollout in progress was reported as a problem")
	}
	if tone := domain.ClassifyCondition("Progressing", "False"); tone != domain.ConditionWarning {
		t.Fatal("a rollout that has stopped progressing was reported as ordinary")
	}
}

func TestAnUnrecognisedConditionIsLeftAlone(t *testing.T) {
	// Safer than guessing. An operator's own controller may report anything,
	// and a wrong colour on a healthy object is worse than no colour — which
	// is precisely the lesson of the node bug above.
	for _, status := range []string{"True", "False", "Unknown"} {
		if tone := domain.ClassifyCondition("SomethingOperatorSpecific", status); tone != domain.ConditionNormal {
			t.Fatalf("an unknown condition at %s was coloured %q", status, tone)
		}
	}
}

func TestUnknownIsNotAProblemBeingReported(t *testing.T) {
	// Kubernetes allows Unknown and means it: the condition could not be
	// determined. That is a problem being unreportable rather than a problem
	// being reported, and the row's own text says it better than a colour.
	if tone := domain.ClassifyCondition("Ready", "Unknown"); tone != domain.ConditionNormal {
		t.Fatalf("Ready=Unknown coloured %q", tone)
	}
}

func TestAFinishedPodIsNotAFailingOne(t *testing.T) {
	// TWO WARNINGS ON EVERY HEALTHY COMPLETED JOB, which is how a panel
	// teaches people to stop reading it. A Succeeded pod carries Ready=False
	// and ContainersReady=False for ever, correctly — a container that has
	// exited is not ready — and by type and status alone both look like
	// problems. Pod.IsHealthy already special-cases the same thing.
	for _, conditionType := range []string{"Ready", "ContainersReady"} {
		if tone := domain.ClassifyConditionOf(conditionType, "False", "Succeeded"); tone != domain.ConditionNormal {
			t.Errorf("ClassifyConditionOf(%q, False, Succeeded) = %q, want no colour", conditionType, tone)
		}
	}

	// Still a problem on a pod that is supposed to be running.
	if tone := domain.ClassifyConditionOf("Ready", "False", "Running"); tone != domain.ConditionWarning {
		t.Errorf("ClassifyConditionOf(Ready, False, Running) = %q, want a warning", tone)
	}

	// And a terminal pod's OTHER conditions are still worth colouring: only
	// readiness is expected to be False once a pod has finished, so a pod
	// that never got scheduled still says so.
	if tone := domain.ClassifyConditionOf("PodScheduled", "False", "Failed"); tone != domain.ConditionWarning {
		t.Errorf("ClassifyConditionOf(PodScheduled, False, Failed) = %q, want a warning", tone)
	}
}
