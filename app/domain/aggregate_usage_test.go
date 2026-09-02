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
	}, true)

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
	usage := domain.NewAggregateUsage(nil, true)

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
	}, true)

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
	}, true)

	if !usage.Partial() {
		t.Fatalf("measured %d of %d pods and did not report it as partial",
			usage.Measured, usage.Pods)
	}
	if !usage.HasMetrics() {
		t.Fatal("a partly measured workload still has metrics")
	}
}

// controller builds a workload, optionally owned by another one.
func controller(t *testing.T, kind domain.WorkloadKind, name, ownerKind, ownerName string) domain.Workload {
	t.Helper()

	spec := domain.WorkloadSpec{
		Kind:      kind,
		Name:      name,
		Namespace: "web",
		ClusterID: "dev",
	}
	if ownerName != "" {
		spec.Owner = domain.OwnerReference{Kind: ownerKind, Name: ownerName, Controller: true}
	}

	workload, err := domain.NewWorkload(spec)
	if err != nil {
		t.Fatalf("building %s %q: %v", kind, name, err)
	}
	return workload
}

// ownedPod builds a running pod controlled by the named object.
func ownedPod(t *testing.T, name, ownerKind, ownerName string, labels map[string]string) domain.Pod {
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
	if ownerName != "" {
		spec.Owners = []domain.OwnerReference{{Kind: ownerKind, Name: ownerName, Controller: true}}
	}

	pod, err := domain.NewPod(spec)
	if err != nil {
		t.Fatalf("building pod %q: %v", name, err)
	}
	return pod
}

func TestADeploymentIsChargedThroughItsReplicaSets(t *testing.T) {
	// A Deployment does not own its pods. Both sides of a rollout are still
	// counted, because an outgoing ReplicaSet is still owned by the
	// Deployment — those pods are running and costing the cluster.
	web := controller(t, domain.WorkloadDeployment, "web", "", "")
	old := controller(t, domain.WorkloadReplicaSet, "web-abc", "Deployment", "web")
	fresh := controller(t, domain.WorkloadReplicaSet, "web-def", "Deployment", "web")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{web},
		[]domain.Pod{
			ownedPod(t, "web-abc-1", "ReplicaSet", "web-abc", nil),
			ownedPod(t, "web-def-1", "ReplicaSet", "web-def", nil),
		},
		[]domain.Workload{old, fresh},
		true,
	)

	if got := consumption["web/web"].Pods; got != 2 {
		t.Fatalf("charged for %d pods, want both sides of the rollout", got)
	}
	if got := consumption["web/web"].Usage.CPUMilli; got != 60 {
		t.Fatalf("using %dm, want 60m", got)
	}
}

func TestAttributionDoesNotConsultTheSelectorAtAll(t *testing.T) {
	// THE REGRESSION THIS REPLACED. Attribution used to match on the
	// controller's label selector, and the selector reaching the domain had
	// already lost its matchExpressions in the mapper — so a controller
	// selecting purely by expression arrived with an empty one and was
	// charged NOTHING, reporting a healthy workload as running no pods.
	//
	// Ownership does not care what the selector says, or whether there is one.
	web := controller(t, domain.WorkloadDeployment, "web", "", "")
	set := controller(t, domain.WorkloadReplicaSet, "web-abc", "Deployment", "web")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{web},
		[]domain.Pod{ownedPod(t, "web-abc-1", "ReplicaSet", "web-abc", nil)},
		[]domain.Workload{set},
		true,
	)

	if got := consumption["web/web"].Pods; got != 1 {
		t.Fatalf("a controller with no selector was charged for %d pods, want 1", got)
	}
}

func TestSharedLabelsDoNotChargeTheSamePodTwice(t *testing.T) {
	// Two controllers wearing the same labels is legal and is exactly what
	// the selector rule got wrong: each was charged the whole shared set, so
	// the column summed to more than the namespace it was in.
	web := controller(t, domain.WorkloadStatefulSet, "web", "", "")
	other := controller(t, domain.WorkloadStatefulSet, "other", "", "")
	shared := map[string]string{"app": "web"}

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{web, other},
		[]domain.Pod{ownedPod(t, "web-0", "StatefulSet", "web", shared)},
		nil,
		true,
	)

	if got := consumption["web/web"].Pods; got != 1 {
		t.Fatalf("the owner was charged for %d pods, want 1", got)
	}
	if got := consumption["web/other"].Pods; got != 0 {
		t.Fatalf("a controller sharing labels was charged for %d pods, want none", got)
	}
}

func TestACronJobIsChargedThroughItsJobsAndOnlyItsJobs(t *testing.T) {
	// The two-hop case, and the Kind check on it: a Job owned by something
	// else that happens to share a CronJob's name must not be charged to it.
	nightly := controller(t, domain.WorkloadCronJob, "nightly", "", "")
	mine := controller(t, domain.WorkloadJob, "nightly-28001", "CronJob", "nightly")
	theirs := controller(t, domain.WorkloadJob, "workflow-step", "Workflow", "nightly")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{nightly},
		[]domain.Pod{
			ownedPod(t, "nightly-28001-abc", "Job", "nightly-28001", nil),
			ownedPod(t, "workflow-step-xyz", "Job", "workflow-step", nil),
		},
		[]domain.Workload{mine, theirs},
		true,
	)

	if got := consumption["web/nightly"].Pods; got != 1 {
		t.Fatalf("the CronJob was charged for %d pods, want only its own Job's", got)
	}
}

func TestAnOrphanedPodIsChargedToNobody(t *testing.T) {
	// `kubectl delete --cascade=orphan`, or a pod mid-garbage-collection.
	// Charging it to whatever still looks similar reports usage against
	// something that is not causing it.
	web := controller(t, domain.WorkloadStatefulSet, "web", "", "")

	consumption := domain.WorkloadConsumption(
		[]domain.Workload{web},
		[]domain.Pod{ownedPod(t, "orphan", "", "", map[string]string{"app": "web"})},
		nil,
		true,
	)

	if got := consumption["web/web"].Pods; got != 0 {
		t.Fatalf("an orphaned pod was charged to a controller (%d pods)", got)
	}
}

func TestAnIdleSetIsNotAClusterWithoutMetrics(t *testing.T) {
	// THE OTHER REGRESSION THIS FIXES. Collapsing "the cluster served no
	// metrics" into "nothing here measured" had a healthy cluster telling
	// people to install metrics-server every time a CronJob's pods finished.
	metered := domain.NewAggregateUsage(nil, true)
	if !metered.MetricsAvailable {
		t.Fatal("an empty set on a metered cluster reported no metrics source")
	}
	if metered.HasMetrics() {
		t.Fatal("claimed figures with nothing to measure")
	}

	bare := domain.NewAggregateUsage(nil, false)
	if bare.MetricsAvailable {
		t.Fatal("a cluster with no metrics API reported one")
	}
}

func TestFinishedPodsDoNotMakeAReadingPartial(t *testing.T) {
	// metrics-server never reports a Succeeded pod, so counting it as
	// unmeasured made every namespace holding a finished Job permanently
	// "partial" — and had the panel claim the total was short when the
	// missing pods were contributing nothing to it.
	usage := domain.NewAggregateUsage([]domain.Pod{
		usagePod(t, "nightly-1", domain.PodPhaseSucceeded, 0, false),
		usagePod(t, "nightly-2", domain.PodPhaseRunning, 20, true),
	}, true)

	if usage.Pods != 2 {
		t.Fatalf("counted %d pods, want both", usage.Pods)
	}
	if usage.Measurable != 1 {
		t.Fatalf("%d pods measurable, want only the running one", usage.Measurable)
	}
	if usage.Partial() {
		t.Fatal("a finished pod was reported as a missing measurement")
	}
}
