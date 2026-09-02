package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func usagePod(t *testing.T, name string, phase domain.PodPhase, cpuMilli int64, measured bool) domain.Pod {
	t.Helper()

	spec := domain.PodSpec{
		Name:      name,
		Namespace: "web",
		ClusterID: "dev",
		Phase:     phase,
		NodeName:  "node-1",
		Containers: []domain.Container{{
			Name:     "app",
			Requests: domain.Resources{CPUMilli: 100},
			Limits:   domain.Resources{CPUMilli: 500},
		}},
	}
	if measured {
		spec.Usage = domain.NewMetrics(cpuMilli, 0)
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func TestWorkloadUsageSumsItsPods(t *testing.T) {
	usage := domain.NewWorkloadUsage([]domain.Pod{
		usagePod(t, "web-1", domain.PodPhaseRunning, 30, true),
		usagePod(t, "web-2", domain.PodPhaseRunning, 45, true),
	})

	if usage.Usage.CPUMilli != 75 {
		t.Fatalf("usage = %dm, want 75m", usage.Usage.CPUMilli)
	}
	if usage.Requests.CPUMilli != 200 || usage.Limits.CPUMilli != 1000 {
		t.Fatalf("references = %dm requested, %dm limit; want 200m and 1000m",
			usage.Requests.CPUMilli, usage.Limits.CPUMilli)
	}
	if usage.Partial() {
		t.Fatal("reported partial with every pod measured")
	}
}

func TestAControllerWithNothingRunningUsesNothing(t *testing.T) {
	// A Deployment scaled to zero, and a CronJob between runs, are the same
	// case: there are no pods, so there is no consumption. That is a true
	// reading rather than a hole in the chart.
	usage := domain.NewWorkloadUsage(nil)

	if usage.HasMetrics() {
		t.Fatal("claimed a measurement with no pods to measure")
	}
	if usage.Usage.Measured {
		t.Fatal("an unmeasured zero must not be reported as a measured zero")
	}
}

func TestFinishedPodsCountForNothingButAreStillCounted(t *testing.T) {
	// A CronJob's completed pods still exist and are still pods, so they are
	// in Pods — and they have given their reservations back, so a chart drawn
	// against them would show a limit nobody can hit.
	usage := domain.NewWorkloadUsage([]domain.Pod{
		usagePod(t, "nightly-1", domain.PodPhaseSucceeded, 0, false),
		usagePod(t, "nightly-2", domain.PodPhaseRunning, 20, true),
	})

	if usage.Pods != 2 {
		t.Fatalf("counted %d pods, want both", usage.Pods)
	}
	if usage.Requests.CPUMilli != 100 {
		t.Fatalf("requests = %dm, want only the running pod's 100m", usage.Requests.CPUMilli)
	}
}

func TestAPartlyMeasuredWorkloadSaysSo(t *testing.T) {
	// THE CAVEAT THIS EXISTS FOR. Twenty pods where metrics-server answered
	// for eighteen is a real total that is short, and a caption that reports
	// it without saying so overstates what is known.
	usage := domain.NewWorkloadUsage([]domain.Pod{
		usagePod(t, "web-1", domain.PodPhaseRunning, 30, true),
		usagePod(t, "web-2", domain.PodPhaseRunning, 0, false),
	})

	if !usage.Partial() {
		t.Fatalf("measured %d of %d pods and did not report it as partial",
			usage.Measured, usage.Pods)
	}
	if !usage.HasMetrics() {
		t.Fatal("a partly measured workload still has metrics")
	}
}
