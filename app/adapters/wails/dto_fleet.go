package wails

import (
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// One cluster's share of a cross-cluster list, per kind.
//
// Three structs rather than one generic because Wails generates the
// TypeScript from reflection over concrete types, and a type parameter
// reaches it as a name it cannot declare. The header fields are the same on
// all three and are filled by readHeader so they cannot drift.

// ClusterPods is one cluster's share of a cross-cluster pod list.
type ClusterPods struct {
	// Cluster is the cluster the rows came from.
	Cluster string `json:"cluster"`
	// Status is the domain.ClusterReadStatus verdict on the read.
	Status string `json:"status"`
	// Reason is the operator-facing sentence for a status that is not ok or
	// slow — the same sentence a failed single-cluster call would have shown,
	// so the strip cannot say something the tab beside it would not.
	Reason string `json:"reason"`
	// Missing names what a partial read did not get. Empty otherwise.
	Missing []string `json:"missing"`
	// Pods are the rows that arrived.
	Pods []Pod `json:"pods"`
}

// ClusterWorkloads is one cluster's share of a cross-cluster workload list.
type ClusterWorkloads struct {
	Cluster   string     `json:"cluster"`
	Status    string     `json:"status"`
	Reason    string     `json:"reason"`
	Missing   []string   `json:"missing"`
	Workloads []Workload `json:"workloads"`
}

// ClusterEvents is one cluster's share of a cross-cluster event list.
type ClusterEvents struct {
	Cluster string   `json:"cluster"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing"`
	Events  []Event  `json:"events"`
}

// ClusterTable is one cluster's share of a cross-cluster read of an
// ARBITRARY kind.
//
// Its own struct rather than one of the three above, and the difference is
// the columns: a typed row has the same shape everywhere, and a table's
// columns are whatever the API server's printer said on THAT cluster. They
// travel beside that cluster's rows so nothing has to assume two clusters
// printed the same set — a CRD at two versions routinely does not.
type ClusterTable struct {
	Cluster string   `json:"cluster"`
	Status  string   `json:"status"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing"`
	// Columns and Rows are this cluster's own. Both are empty for a cluster
	// that answered nothing — including one that does not serve the kind,
	// which is `unserved` rather than a failure.
	Columns []TableColumn `json:"columns"`
	Rows    []TableRow    `json:"rows"`
	// Truncated reports that THIS cluster's read stopped at Cap with objects
	// left unread. Per cluster, because the cap is per read: in a merged
	// table one cluster can be complete and the next a prefix, and a single
	// flag for the whole table could only be a lie in one direction or the
	// other.
	Truncated bool `json:"truncated"`
	// Cap is the limit that stopped it, zero when nothing did.
	Cap int `json:"cap"`
}

// toClusterTables projects one table per cluster, keeping each cluster's
// columns with its own rows.
func toClusterTables(reads []domain.ClusterRead[domain.ResourceTable]) []ClusterTable {
	out := make([]ClusterTable, len(reads))
	for i, read := range reads {
		cluster, status, reason, missing := readHeader(read)
		entry := ClusterTable{
			Cluster: cluster,
			Status:  status,
			Reason:  reason,
			Missing: missing,
			Columns: []TableColumn{},
			Rows:    []TableRow{},
		}

		// One item at most — see FleetService.ListTable for why a table
		// travels as a one-element slice.
		if len(read.Items) > 0 {
			table := toResourceTable(read.Items[0])
			if table.Columns != nil {
				entry.Columns = table.Columns
			}
			if table.Rows != nil {
				entry.Rows = table.Rows
			}
			entry.Truncated = table.Truncated
			entry.Cap = table.Cap
		}
		out[i] = entry
	}
	return out
}

// readHeader projects the part of a ClusterRead every kind shares.
//
// The reason goes through classifyError like every other error that reaches
// the frontend: the log already has the whole chain, and the operator gets
// the one sentence. Missing is never nil on the wire, so the frontend can
// iterate it without a guard.
func readHeader[T any](read domain.ClusterRead[T]) (cluster, status, reason string, missing []string) {
	if read.Err != nil {
		_, reason = classifyError(read.Err)
	}
	missing = read.Missing
	if missing == nil {
		missing = []string{}
	}
	return string(read.Cluster), string(read.Status), reason, missing
}

func toClusterPods(reads []domain.ClusterRead[domain.Pod], now time.Time) []ClusterPods {
	out := make([]ClusterPods, len(reads))
	for i, read := range reads {
		cluster, status, reason, missing := readHeader(read)
		out[i] = ClusterPods{
			Cluster: cluster,
			Status:  status,
			Reason:  reason,
			Missing: missing,
			Pods:    toPods(read.Items, now),
		}
	}
	return out
}

func toClusterWorkloads(reads []domain.ClusterRead[domain.Workload], now time.Time) []ClusterWorkloads {
	out := make([]ClusterWorkloads, len(reads))
	for i, read := range reads {
		cluster, status, reason, missing := readHeader(read)
		out[i] = ClusterWorkloads{
			Cluster:   cluster,
			Status:    status,
			Reason:    reason,
			Missing:   missing,
			Workloads: toWorkloads(read.Items, now),
		}
	}
	return out
}

func toClusterEvents(reads []domain.ClusterRead[domain.Event], now time.Time) []ClusterEvents {
	out := make([]ClusterEvents, len(reads))
	for i, read := range reads {
		cluster, status, reason, missing := readHeader(read)
		out[i] = ClusterEvents{
			Cluster: cluster,
			Status:  status,
			Reason:  reason,
			Missing: missing,
			Events:  toEvents(read.Items, now),
		}
	}
	return out
}
