package domain

// This file answers "what is this set of pods costing", which is the question
// behind three different rows: a namespace, a controller in a list, and a
// controller's own panel. One type rather than three, because the arithmetic
// and — more importantly — the rules about what counts are identical, and
// three copies of a rule is three chances to get one of them wrong.

// AggregateUsage is what a set of pods is consuming, against what they
// reserved and what they will be stopped at.
//
// NOTHING ABOVE A POD HAS USAGE OF ITS OWN. A namespace is a name and a
// controller is a template; the consumption belongs to whatever pods exist
// under them at this moment. That decides the honest answers to the awkward
// cases — a Deployment scaled to zero, a CronJob between runs and an empty
// namespace are all using nothing, because nothing of them is running, and
// that is a reading rather than a hole.
type AggregateUsage struct {
	// Pods is how many pods the figures cover.
	Pods int
	// Measurable is how many of those COULD be measured — the ones occupying a
	// node.
	//
	// Not every pod can be. metrics-server never reports a Succeeded or
	// Failed pod, and never reports one that has not been scheduled, so
	// measuring against Pods made every namespace holding a finished Job
	// permanently "partial" and had the panel claim its total was short when
	// the missing pods were using nothing.
	Measurable int
	// Measured is how many of the measurable ones reported a measurement.
	//
	// A COUNT RATHER THAN A FLAG, because it is the caveat: twenty pods where
	// metrics-server answered for eighteen is a total that is real and short,
	// and reporting it without saying so overstates what is known.
	Measured int
	// MetricsAvailable reports whether the CLUSTER served metrics at all.
	//
	// SEPARATE FROM Measured, and the distinction is the whole point. A
	// cluster with no metrics-server and an idle namespace on a fully metered
	// one both measure nothing, and they are not the same thing to tell
	// somebody: one is "install metrics-server", the other is "nothing is
	// running". Collapsing them had a healthy cluster reporting it had no
	// metrics source every time a CronJob's pods had all finished.
	MetricsAvailable bool
	// Usage is the sum over the pods that reported one.
	Usage Metrics
	// Requests and Limits are summed over the pods that OCCUPY A NODE, which
	// is not every pod: a Succeeded Job pod still exists and still counts in
	// Pods, and has given its reservation back.
	Requests Resources
	Limits   Resources
}

// HasMetrics reports whether there are figures to draw.
func (u AggregateUsage) HasMetrics() bool { return u.Measured > 0 }

// Partial reports whether some of the pods that COULD have been measured were
// not, which is the case a caller has to say out loud rather than quietly
// total. Finished pods are not counted against it: they are not missing from
// the total, they are contributing nothing to it.
func (u AggregateUsage) Partial() bool { return u.Measured > 0 && u.Measured < u.Measurable }

// CPUPercent returns measured CPU as a percentage of what was requested, or 0
// when nothing was requested — which is not 0% of anything and callers must
// check HasCPURequest before drawing it.
func (u AggregateUsage) CPUPercent() float64 {
	return percentOf(u.Usage.CPUMilli, u.Requests.CPUMilli, u.HasMetrics())
}

// MemoryPercent is the same against requested memory.
func (u AggregateUsage) MemoryPercent() float64 {
	return percentOf(u.Usage.MemoryBytes, u.Requests.MemoryBytes, u.HasMetrics())
}

// CPULimitPercent returns measured CPU against the limit, which is the ratio
// that predicts throttling rather than the one that predicts a scheduling
// problem.
func (u AggregateUsage) CPULimitPercent() float64 {
	return percentOf(u.Usage.CPUMilli, u.Limits.CPUMilli, u.HasMetrics())
}

// MemoryLimitPercent is the same against the memory limit — the only one of
// the four that predicts a kill.
func (u AggregateUsage) MemoryLimitPercent() float64 {
	return percentOf(u.Usage.MemoryBytes, u.Limits.MemoryBytes, u.HasMetrics())
}

// HasCPURequest and its three siblings report whether there is a denominator
// at all. Separate from a zero percentage, because an idle set of pods that
// DID reserve CPU also reads 0% and the two must not draw the same thing.
func (u AggregateUsage) HasCPURequest() bool    { return u.Requests.CPUMilli > 0 }
func (u AggregateUsage) HasMemoryRequest() bool { return u.Requests.MemoryBytes > 0 }
func (u AggregateUsage) HasCPULimit() bool      { return u.Limits.CPUMilli > 0 }
func (u AggregateUsage) HasMemoryLimit() bool   { return u.Limits.MemoryBytes > 0 }

func percentOf(used, against int64, measured bool) float64 {
	if !measured || against <= 0 {
		return 0
	}
	return float64(used) / float64(against) * 100
}

// NewAggregateUsage sums pods into one reading.
//
// metricsAvailable is what the CLUSTER said, not what these pods did — see
// MetricsAvailable for why the two must not be collapsed.
func NewAggregateUsage(pods []Pod, metricsAvailable bool) AggregateUsage {
	usage := AggregateUsage{Pods: len(pods), MetricsAvailable: metricsAvailable}

	for _, pod := range pods {
		if measured := pod.Usage(); measured.Measured {
			usage.Measured++
			usage.Usage.CPUMilli += measured.CPUMilli
			usage.Usage.MemoryBytes += measured.MemoryBytes
		}

		// The same line that decides whether a pod reserves anything decides
		// whether it can be measured, and for the same reason: a pod that has
		// finished or has not been scheduled is not on a node for either.
		if !pod.OccupiesNode() {
			continue
		}
		usage.Measurable++
		usage.Requests = usage.Requests.Add(pod.Requests())
		usage.Limits = usage.Limits.Add(pod.Limits())
	}

	usage.Usage.Measured = usage.Measured > 0
	return usage
}

// WorkloadConsumption attributes pods to the controllers in a list and sums
// each one, keyed by "namespace/name".
//
// BY THE POD'S CONTROLLING ownerReference, resolved through one intermediate
// where Kubernetes puts one: a Deployment's pods are owned by its
// ReplicaSets, a CronJob's by its Jobs, and everything else owns its pods
// directly. The caller passes those intermediates; see
// WorkloadService.WorkloadConsumption.
//
// THIS USED TO MATCH ON THE CONTROLLER'S LABEL SELECTOR, and that was a
// mistake worth recording. Ownership is what the objects themselves report;
// a selector is what a controller uses to FIND pods, and the two differ in
// every case that matters — two controllers with overlapping selectors are
// each charged the whole shared set, a bare ReplicaSet wearing a Deployment's
// labels is charged to the Deployment, and a pod orphaned by
// `--cascade=orphan` is charged to whatever still matches it. Worse, the
// selector this saw had already lost its matchExpressions on the way through
// the mapper, so a controller selecting purely by expression matched nothing
// and reported a healthy workload as running no pods at all.
//
// It was also not buying anything. The argument for it was that the
// ownerReference chain would cost a ReplicaSet list per refresh — while the
// same refresh already lists every POD in the namespace, which is an order of
// magnitude larger.
//
// An outgoing ReplicaSet is still owned by its Deployment, so a rollout's
// pods on both sides are still counted, which was the one thing the selector
// got right.
//
// A pod whose controller is not in the list, and not owned by anything in it,
// is charged to NOBODY. That is the expensive direction to get right: charging
// an unattributable pod to a controller that merely looks similar reports
// usage against something that is not causing it.
func WorkloadConsumption(workloads []Workload, pods []Pod, intermediates []Workload, metricsAvailable bool) map[string]AggregateUsage {
	wanted := make(map[string]bool, len(workloads))
	for _, workload := range workloads {
		wanted[attributionKey(workload.Namespace(), string(workload.Kind()), workload.Name())] = true
	}

	// Each intermediate's own controller, so a pod can be followed one hop
	// further up. Kind is checked on both ends: a Job owned by an Argo
	// Workflow that happens to share a CronJob's name must not be charged to
	// the CronJob.
	parentOf := make(map[string]string, len(intermediates))
	for _, intermediate := range intermediates {
		owner := intermediate.Owner()
		if owner.IsZero() {
			continue
		}
		child := attributionKey(intermediate.Namespace(), string(intermediate.Kind()), intermediate.Name())
		parentOf[child] = attributionKey(intermediate.Namespace(), owner.Kind, owner.Name)
	}

	byWorkload := make(map[string][]Pod, len(workloads))
	for _, pod := range pods {
		controller := Controller(pod.Owners())
		if controller.IsZero() {
			continue
		}

		direct := attributionKey(pod.Namespace(), controller.Kind, controller.Name)
		if wanted[direct] {
			byWorkload[direct] = append(byWorkload[direct], pod)
			continue
		}
		if parent, known := parentOf[direct]; known && wanted[parent] {
			byWorkload[parent] = append(byWorkload[parent], pod)
		}
	}

	consumption := make(map[string]AggregateUsage, len(workloads))
	for _, workload := range workloads {
		held := byWorkload[attributionKey(workload.Namespace(), string(workload.Kind()), workload.Name())]
		consumption[workload.Namespace().String()+"/"+workload.Name()] = NewAggregateUsage(held, metricsAvailable)
	}
	return consumption
}

// attributionKey identifies one object for attribution. Kind-qualified, because a
// Job and a ReplicaSet in one namespace may share a name and own different
// pods.
func attributionKey(namespace NamespaceName, kind, name string) string {
	return namespace.String() + "/" + kind + "/" + name
}
