package domain

// ClusterReadStatus says how one cluster answered a read made across several
// of them at once.
//
// Modelled on MetricsStatus, and for the same reason: an empty share of a
// merged table is not one situation. A cluster whose account may not list
// pods, one that is off the network and one that is merely slow need
// opposite advice, and a merged table that showed the same nothing for all
// three would send somebody to check a VPN over a permission problem.
type ClusterReadStatus string

const (
	// ClusterReadOK means the cluster answered in full.
	ClusterReadOK ClusterReadStatus = "ok"
	// ClusterReadPartial means the cluster answered some of what was asked
	// and refused the rest — an account that may list Deployments but not
	// CronJobs. Missing names what did not arrive; Items is what did.
	ClusterReadPartial ClusterReadStatus = "partial"
	// ClusterReadSlow means the cluster had not answered when the others
	// had, and was not waited for. Its read is left running and what it
	// eventually returns is handed to the next read of the same thing, so
	// Items may carry that late answer; until one exists, the caller keeps
	// showing what it last had.
	ClusterReadSlow ClusterReadStatus = "slow"
	// ClusterReadForbidden means RBAC refused the read. The rows are not
	// missing, they are not permitted — an administrator question.
	ClusterReadForbidden ClusterReadStatus = "forbidden"
	// ClusterReadUnreachable means the API server could not be contacted, or
	// did not answer before the request's deadline. Usually transient: a
	// VPN, a laptop waking, a port-forward that closed.
	ClusterReadUnreachable ClusterReadStatus = "unreachable"
	// ClusterReadFailed means the read failed for some other reason,
	// including the cluster having been disconnected mid-read.
	ClusterReadFailed ClusterReadStatus = "failed"
)

// ClusterRead is one cluster's share of a read made across several.
//
// A slice of these, one per cluster in tab order, is what a merged table is
// built from. Each carries its own verdict so one refusal cannot empty the
// others — the rule the overview already follows for its sources, applied
// across clusters instead of across kinds.
type ClusterRead[T any] struct {
	// Cluster is which cluster this is.
	Cluster ClusterID
	// Status is the verdict on the read.
	Status ClusterReadStatus
	// Err is what failed, whole, for the log. Nil when Status is OK or Slow.
	// What of it an operator sees is the adapter's decision, as for every
	// other error that crosses to the frontend.
	Err error
	// Missing names what a partial read did not get — workload kinds, for a
	// workload read. Empty unless Status is Partial.
	Missing []string
	// Items are the rows that did arrive: everything on a full read, some
	// on a partial one, none on a refused one. A slow read carries the late
	// answer of the read before it, when there is one.
	Items []T
}

// FleetWorkloadKinds are the controller kinds a cross-cluster workload list
// reads: every kind but ReplicaSet.
//
// A ReplicaSet is an intermediate — a Deployment's current template and
// however many previous ones it keeps — and a merged list that carried them
// would show every Deployment several times over, mostly at zero replicas.
// On the 201-pod cluster measured for the watch there were 186 of them. The
// per-cluster ReplicaSet page still exists for anyone who wants that.
func FleetWorkloadKinds() []WorkloadKind {
	return []WorkloadKind{
		WorkloadDeployment,
		WorkloadStatefulSet,
		WorkloadDaemonSet,
		WorkloadJob,
		WorkloadCronJob,
	}
}
