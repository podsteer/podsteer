package ports

import (
	"context"

	"github.com/podsteer/podsteer/app/domain"
)

// TopologyPort reads what a namespace topology is drawn from.
//
// Kept in its own file and its own interface so the topology can be wired,
// faked and reviewed without touching the larger workload port.
type TopologyPort interface {
	// TopologySources reads every source of one scope, concurrently. A read
	// that is refused or fails names itself in TopologyInput.Unreadable and
	// the rest carries on: an error is returned only when no client could be
	// built for the cluster at all. Secrets are NEVER listed — the names a
	// pod template gives are the whole of what the topology knows of them.
	TopologySources(ctx context.Context, id domain.ClusterID, scope domain.TopologyScope) (domain.TopologyInput, error)
}

// ChangeSink hears that something in a cluster may have changed.
//
// Called from the watch stores' event handlers and from every write's
// dropping of the cluster's cached reads, so an implementation MUST NOT
// BLOCK: it runs on a reflector's delivery goroutine and on the write path.
// A namespace of NamespaceAll means "somewhere in the cluster".
type ChangeSink interface {
	Changed(id domain.ClusterID, namespace domain.NamespaceName)
}

// TopologyService is the topology use case the frontend drives.
type TopologyService interface {
	// Topology draws one scope of one open cluster. Drawing a scope is also
	// what registers interest in it: changes are announced only for the
	// clusters and namespaces somebody has drawn.
	Topology(ctx context.Context, id domain.ClusterID, scope domain.TopologyScope) (domain.TopologyGraph, error)
	// Subscribe receives coalesced changes in drawn scopes until cancel.
	Subscribe(fn func(domain.ClusterChange)) (cancel func())
	// Release forgets a cluster's drawn scope, so its changes stop being
	// announced.
	Release(id domain.ClusterID)
}
