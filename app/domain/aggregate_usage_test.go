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
	usage := domain.NewAggregateUsage([]domain.Pod{
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
	usage := domain.NewAggregateUsage(nil)

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
	usage := domain.NewAggregateUsage([]domain.Pod{
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
	usage := domain.NewAggregateUsage([]domain.Pod{
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

// workloadWith builds a controller with a selector, or one owned by another.
func workloadWith(t *testing.T, kind domain.WorkloadKind, name string, selector map[string]string, owner string) domain.Workload {
	t.Helper()

	spec := domain.WorkloadSpec{
		Kind:      kind,
		Name:      name,
		Namespace: "web",
		ClusterID: "dev",
		Selector:  selector,
	}
	if owner != "" {
		spec.Owner = domain.OwnerReference{Kind: "CronJob", Name: owner, Controller: true}
	}

	workload, err := domain.NewWorkload(spec)
	if err != nil {
		t.Fatalf("building %s %q: %v", kind, name, err)
	}
	return workload
}

// labelledPod builds a running pod with labels and a controller.
func labelledPod(t *testing.T, name string, labels map[string]string, controller string) domain.Pod {
	t.Helper()

	spec := domain.PodSpec{
		Name:       name,
		Namespace:  "web",
		ClusterID:  "dev",
		Phase:      domain.PodPhaseRunning,
		NodeName:   "node-1",
		Labels:     labels,
		Containers: []domain.Container{{Name: "app", Requests: domain.Resources{CPUMilli: 100}}},
		Usage:      domain.NewMetrics(30, 0),
	}
	if controller != "" {
		spec.Owners = []domain.OwnerReference{{Kind: "Job", Name: controller, Controller: true}}
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func TestAControllerIsChargedForThePodsItsSelectorMatches(t *testing.T) {
	// A Deployment does not own its pods — its ReplicaSets do — so the
	// selector is what attributes them, and mid-rollout it correctly picks up
	// the outgoing ReplicaSet's pods as well. Those are running and costing
	// the cluster; a figure that ignored them would understate a rollout at
	// the moment somebody is watching one.
	web := workloadWith(t, domain.WorkloadDeployment, "web", map[string]string{"app": "web"}, "")
	api := workloadWith(t, domain.WorkloadDeployment, "api", map[string]string{"app": "api"}, "")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{web, api},
		[]domain.Pod{
			labelledPod(t, "web-old-1", map[string]string{"app": "web", "pod-template-hash": "1"}, ""),
			labelledPod(t, "web-new-1", map[string]string{"app": "web", "pod-template-hash": "2"}, ""),
			labelledPod(t, "api-1", map[string]string{"app": "api"}, ""),
		},
		nil,
	)

	if got := consumption["web/web"].Pods; got != 2 {
		t.Fatalf("web charged for %d pods, want both sides of the rollout", got)
	}
	if got := consumption["web/web"].Usage.CPUMilli; got != 60 {
		t.Fatalf("web using %dm, want 60m", got)
	}
	if got := consumption["web/api"].Pods; got != 1 {
		t.Fatalf("api charged for %d pods, want 1", got)
	}
}

func TestAControllerWithNoSelectorIsChargedThroughWhatItOwns(t *testing.T) {
	// A CronJob has no selector: its Jobs own the pods and it owns the Jobs.
	nightly := workloadWith(t, domain.WorkloadCronJob, "nightly", nil, "")
	run := workloadWith(t, domain.WorkloadJob, "nightly-28001", nil, "nightly")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{nightly},
		[]domain.Pod{labelledPod(t, "nightly-28001-abc", nil, "nightly-28001")},
		[]domain.Workload{run},
	)

	if got := consumption["web/nightly"].Pods; got != 1 {
		t.Fatalf("the CronJob was charged for %d pods, want its Job's 1", got)
	}
}

func TestAnEmptySelectorIsChargedNothingRatherThanEverything(t *testing.T) {
	// THE FAILURE THIS GUARDS, and it is the expensive direction to get
	// wrong. A controller PodSteer cannot attribute pods to must be charged
	// nothing; charging it the whole namespace would report every workload as
	// using everything.
	orphan := workloadWith(t, domain.WorkloadDeployment, "orphan", nil, "")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{orphan},
		[]domain.Pod{labelledPod(t, "someone-else", map[string]string{"app": "web"}, "")},
		nil,
	)

	if got := consumption["web/orphan"].Pods; got != 0 {
		t.Fatalf("an unattributable controller was charged for %d pods", got)
	}
}
