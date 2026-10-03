package domain

import "time"

// FleetPodPage is one page of the merged All-clusters pod list, and every
// open cluster's own verdict beside it.
//
// The verdicts are not decoration: a cluster that refused, timed out or is
// slow answers for itself in the status strip, and its share of the list is
// whatever that verdict allows (see FleetPodShare). Paging the merged list in
// Go must not turn "this cluster did not answer" into "this cluster has
// nothing".
type FleetPodPage struct {
	PodPage
	// Clusters are the clusters read, in tab order.
	Clusters []FleetPodShare
}

// FleetPodShare is one cluster's contribution to the merged pod list.
type FleetPodShare struct {
	Cluster ClusterID
	Status  ClusterReadStatus
	// Err is what failed, whole, for the adapter to phrase.
	Err error
	// Missing names what a partial read did not get.
	Missing []string
	// Rows counts the rows this cluster contributes before any filter —
	// including rows kept from an earlier answer while it is slow or
	// unreachable.
	Rows int
	// RowsAt is when those rows were read; zero when there are none.
	RowsAt time.Time
	// Stale says the rows are older than this read: kept from an earlier
	// answer because this one brought none.
	Stale bool
}
