package application

import (
	"context"
	"fmt"
	"time"

	"github.com/podsteer/podsteer/app/domain"
)

// QueryPods answers one page of a namespace's pod list — the table's search,
// chips, sort and page — instead of the whole list.
//
// THE SAME READ ListPods MAKES, then domain.QueryPods over it. The read is
// the read cache's (keyed by the projection, as every list is), so a page
// query and the assessment's own pod list landing in one tick are still one
// request; what changes is that one page of rows and the counts around it
// cross the bridge, not every row the namespace holds. See CLAUDE.md, "The
// pod table is paged in Go".
func (s *WorkloadService) QueryPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection, query domain.PodQuery) (domain.PodPage, error) {
	return s.QueryPodsIn(ctx, id, domain.ScopeOf(namespace), projection, query)
}

// QueryPodsIn is QueryPods over a scope of namespaces: the page is cut from
// the MERGED list, so sort, search and paging see every namespace at once.
func (s *WorkloadService) QueryPodsIn(ctx context.Context, id domain.ClusterID, scope domain.NamespaceScope, projection domain.Projection, query domain.PodQuery) (domain.PodPage, error) {
	query, err := domain.NewPodQuery(query)
	if err != nil {
		return domain.PodPage{}, fmt.Errorf("querying pods: %w", err)
	}

	pods, err := s.ListPodsIn(ctx, id, scope, projection)
	if err != nil {
		return domain.PodPage{}, err
	}

	return domain.QueryPods(pods, query, time.Now(), s.textOrder), nil
}

// MatchingPods is every pod the query keeps, in the table's order, with the
// page ignored — for the two reads that are about the whole match on
// purpose and happen on a gesture rather than a tick: the CSV export and the
// keys behind "select all matching". The page size is not validated, since
// there is no page.
func (s *WorkloadService) MatchingPods(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection, query domain.PodQuery) ([]domain.Pod, error) {
	return s.MatchingPodsIn(ctx, id, domain.ScopeOf(namespace), projection, query)
}

// MatchingPodsIn is MatchingPods over a scope of namespaces.
func (s *WorkloadService) MatchingPodsIn(ctx context.Context, id domain.ClusterID, scope domain.NamespaceScope, projection domain.Projection, query domain.PodQuery) ([]domain.Pod, error) {
	query.Offset, query.Limit = 0, domain.MaxPodPageSize
	query, err := domain.NewPodQuery(query)
	if err != nil {
		return nil, fmt.Errorf("matching pods: %w", err)
	}

	pods, err := s.ListPodsIn(ctx, id, scope, projection)
	if err != nil {
		return nil, err
	}

	return domain.MatchingPods(pods, query, time.Now(), s.textOrder), nil
}

// ListPodKeys names every pod the query keeps — namespace, name, UID and
// controller — for "select all matching" across pages. A fraction of the
// full rows, and asked for once, when somebody presses it.
func (s *WorkloadService) ListPodKeys(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, projection domain.Projection, query domain.PodQuery) ([]domain.PodKey, error) {
	return s.ListPodKeysIn(ctx, id, domain.ScopeOf(namespace), projection, query)
}

// ListPodKeysIn is ListPodKeys over a scope of namespaces.
func (s *WorkloadService) ListPodKeysIn(ctx context.Context, id domain.ClusterID, scope domain.NamespaceScope, projection domain.Projection, query domain.PodQuery) ([]domain.PodKey, error) {
	pods, err := s.MatchingPodsIn(ctx, id, scope, projection, query)
	if err != nil {
		return nil, err
	}

	keys := make([]domain.PodKey, 0, len(pods))
	for _, pod := range pods {
		keys = append(keys, domain.KeyOf(pod))
	}
	return keys, nil
}
