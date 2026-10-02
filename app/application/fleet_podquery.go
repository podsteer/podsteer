package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// keptPods is a cluster's last pod rows, held for the read that brings none.
type keptPods struct {
	namespace domain.NamespaceName
	pods      []domain.Pod
	at        time.Time
}

// podMemo holds each cluster's last good pod rows.
//
// IT IS THE FRONTEND'S mergeFleet, MOVED. While the merged table held every
// row in the webview, a slow or unreachable cluster kept its previous rows
// on screen, marked stale; a refused one lost them (stale rows under a
// "forbidden" mark claim a view the account does not have). With the page
// cut in Go the webview no longer has those rows to keep, so they are kept
// here, by the same three rules — see sharePods.
//
// One entry per cluster, for the namespace last read: the frontend dropped
// everything when the window's namespace changed, and an entry for another
// namespace is simply not used. Invalidate drops a cluster's entry with its
// late answers, for the same reason.
type podMemo struct {
	mu   sync.Mutex
	kept map[domain.ClusterID]keptPods
}

// share applies mergeFleet's rules to one cluster's read: a read that
// brought rows replaces them; a slow or unreachable one keeps the last rows
// for the same namespace, marked stale; anything else — refused, failed, not
// served — shows none and forgets them.
func (m *podMemo) share(read domain.ClusterRead[domain.Pod], namespace domain.NamespaceName, now time.Time) (domain.FleetPodShare, []domain.Pod) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kept == nil {
		m.kept = make(map[domain.ClusterID]keptPods, 4)
	}

	share := domain.FleetPodShare{Cluster: read.Cluster, Status: read.Status, Err: read.Err, Missing: read.Missing}

	switch {
	case read.Status == domain.ClusterReadOK, read.Status == domain.ClusterReadPartial,
		read.Status == domain.ClusterReadSlow && len(read.Items) > 0:
		m.kept[read.Cluster] = keptPods{namespace: namespace, pods: read.Items, at: now}
		share.Rows, share.RowsAt = len(read.Items), now
		return share, read.Items

	case read.Status == domain.ClusterReadSlow, read.Status == domain.ClusterReadUnreachable:
		last, ok := m.kept[read.Cluster]
		if !ok || last.namespace != namespace {
			return share, nil
		}
		share.Rows, share.RowsAt, share.Stale = len(last.pods), last.at, len(last.pods) > 0
		return share, last.pods

	default:
		delete(m.kept, read.Cluster)
		return share, nil
	}
}

func (m *podMemo) forget(id domain.ClusterID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.kept, id)
}

// QueryPods answers one page of the merged pod list across the named
// clusters, with every cluster's verdict.
//
// The SAME fan-out ListPods makes — the same per-cluster read, the same
// budget, the same late answers — then each cluster's share by podMemo's
// rules, concatenated in tab order (the merged table's own order when
// nothing is sorted), then domain.QueryPods over the lot. query.Clusters is
// the strip's selection and narrows the rows, not the reads: a deselected
// cluster is still read, because its chip still reports on it.
func (s *FleetService) QueryPods(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName, query domain.PodQuery) (domain.FleetPodPage, error) {
	query, err := domain.NewPodQuery(query)
	if err != nil {
		return domain.FleetPodPage{}, fmt.Errorf("querying pods across clusters: %w", err)
	}

	shares, pods, err := s.mergedPods(ctx, ids, namespace)
	if err != nil {
		return domain.FleetPodPage{}, err
	}

	return domain.FleetPodPage{
		PodPage:  domain.QueryPods(pods, query, time.Now(), s.textOrder),
		Clusters: shares,
	}, nil
}

// MatchingPods is every pod of the merged list the query keeps, page
// ignored — for the merged table's CSV export.
func (s *FleetService) MatchingPods(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName, query domain.PodQuery) ([]domain.Pod, error) {
	query.Offset, query.Limit = 0, domain.MaxPodPageSize
	query, err := domain.NewPodQuery(query)
	if err != nil {
		return nil, fmt.Errorf("matching pods across clusters: %w", err)
	}

	_, pods, err := s.mergedPods(ctx, ids, namespace)
	if err != nil {
		return nil, err
	}
	return domain.MatchingPods(pods, query, time.Now(), s.textOrder), nil
}

// mergedPods reads every cluster and returns each one's share and all of
// their rows, in tab order.
func (s *FleetService) mergedPods(ctx context.Context, ids []domain.ClusterID, namespace domain.NamespaceName) ([]domain.FleetPodShare, []domain.Pod, error) {
	reads, err := s.ListPods(ctx, ids, namespace)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	shares := make([]domain.FleetPodShare, 0, len(reads))
	var pods []domain.Pod
	for _, read := range reads {
		share, rows := s.podMemo.share(read, namespace, now)
		shares = append(shares, share)
		pods = append(pods, rows...)
	}
	return shares, pods, nil
}
