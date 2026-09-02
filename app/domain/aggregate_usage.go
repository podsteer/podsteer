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
	// Measured is how many of those reported a measurement.
	//
	// A COUNT RATHER THAN A FLAG, because it is the caveat: twenty pods where
	// metrics-server answered for eighteen is a total that is real and short,
	// and reporting it without saying so overstates what is known.
	Measured int
	// Usage is the sum over the pods that reported one.
	Usage Metrics
	// Requests and Limits are summed over the pods that OCCUPY A NODE, which
	// is not every pod: a Succeeded Job pod still exists and still counts in
	// Pods, and has given its reservation back.
	Requests Resources
	Limits   Resources
}

// HasMetrics reports whether anything measured at all.
func (u AggregateUsage) HasMetrics() bool { return u.Measured > 0 }

// Partial reports whether some of the pods went unmeasured, which is the case
// a caller has to say out loud rather than quietly total.
func (u AggregateUsage) Partial() bool { return u.Measured > 0 && u.Measured < u.Pods }

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
func NewAggregateUsage(pods []Pod) AggregateUsage {
	usage := AggregateUsage{Pods: len(pods)}

	for _, pod := range pods {
		if measured := pod.Usage(); measured.Measured {
			usage.Measured++
			usage.Usage.CPUMilli += measured.CPUMilli
			usage.Usage.MemoryBytes += measured.MemoryBytes
		}

		if !pod.OccupiesNode() {
			continue
		}
		usage.Requests = usage.Requests.Add(pod.Requests())
		usage.Limits = usage.Limits.Add(pod.Limits())
	}

	usage.Usage.Measured = usage.Measured > 0
	return usage
}

// WorkloadConsumption attributes pods to the controllers in a list and sums
// each one, keyed by "namespace/name".
//
// HOW A POD IS ATTRIBUTED, and why it is not simply the ownerReference.
//
// A Deployment does not own its pods; its ReplicaSets do. Following the chain
// would mean listing every ReplicaSet in the namespace on every refresh just
// to read a number, so the label SELECTOR is used instead — which is the same
// thing the Deployment itself uses to find them, and which correctly includes
// the pods of an outgoing ReplicaSet mid-rollout. Those pods are running and
// costing the cluster; a figure that ignored them would understate a rollout
// at exactly the moment somebody is watching one.
//
// A CronJob is the exception, because it has no selector: its Jobs own the
// pods and it owns the Jobs, so the caller passes the namespace's Jobs as
// `intermediates` and the two hops are joined here.
//
// An empty selector matches NOTHING, which is the opposite of the Kubernetes
// API's own convention for an empty selector in some other contexts. A
// controller with no selector is one this cannot attribute, and attributing
// every pod in the namespace to it would be far worse than attributing none.
func WorkloadConsumption(workloads []Workload, pods []Pod, intermediates []Workload) map[string]AggregateUsage {
	byWorkload := make(map[string][]Pod, len(workloads))

	// The name a Job's pods should be counted under, for CronJobs.
	ownerOf := make(map[string]string, len(intermediates))
	for _, intermediate := range intermediates {
		if owner := intermediate.Owner(); owner.Name != "" {
			ownerOf[intermediate.Namespace().String()+"/"+intermediate.Name()] = owner.Name
		}
	}

	for _, workload := range workloads {
		key := workload.Namespace().String() + "/" + workload.Name()

		for _, pod := range pods {
			if pod.Namespace() != workload.Namespace() {
				continue
			}
			if attributes(workload, pod, ownerOf) {
				byWorkload[key] = append(byWorkload[key], pod)
			}
		}
	}

	consumption := make(map[string]AggregateUsage, len(workloads))
	for _, workload := range workloads {
		key := workload.Namespace().String() + "/" + workload.Name()
		consumption[key] = NewAggregateUsage(byWorkload[key])
	}
	return consumption
}

// attributes reports whether a pod belongs to a workload.
func attributes(workload Workload, pod Pod, ownerOf map[string]string) bool {
	if selector := workload.Selector(); len(selector) > 0 {
		return selectorMatches(selector, pod.Labels())
	}

	// No selector: the two-hop case. The pod's controller is an object this
	// workload owns — a Job under a CronJob — so the pod is counted here.
	controller := controllingOwner(pod)
	if controller == "" {
		return false
	}
	return ownerOf[pod.Namespace().String()+"/"+controller] == workload.Name()
}

// controllingOwner returns the name of the object that will recreate a pod.
func controllingOwner(pod Pod) string {
	for _, owner := range pod.Owners() {
		if owner.Controller {
			return owner.Name
		}
	}
	return ""
}
