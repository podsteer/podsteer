package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// CountResources reports how many objects of a kind a namespace holds.
//
// THE WHOLE TRICK IS `limit=1`. A LIST that is cut short reports how many
// objects were left behind in `metadata.remainingItemCount`, so one object
// plus that number is the total — and the request costs the same whether the
// namespace holds three Secrets or thirty thousand. Counting by listing
// everything and taking len() would pull every Secret's contents across the
// wire to arrive at a number, which is both slow and precisely the read
// pattern a cluster's audit rules are written to notice.
//
// No label or field selector, deliberately: the API server leaves
// remainingItemCount unset for a filtered list, because it cannot know how
// many of the objects it skipped would have matched.
func (a *Adapter) CountResources(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName) (int, error) {
	op := fmt.Sprintf("counting %s in %q of %q", kind.Resource, namespace, id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return 0, err
	}

	gvr := schema.GroupVersionResource{
		Group:    kind.Group,
		Version:  kind.Version,
		Resource: kind.Resource,
	}

	var client dynamic.ResourceInterface = set.dynamic.Resource(gvr)
	if kind.Namespaced {
		client = set.dynamic.Resource(gvr).Namespace(namespace.String())
	}

	list, err := client.List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return 0, classify(op, err)
	}

	if remaining := list.GetRemainingItemCount(); remaining != nil {
		return len(list.Items) + int(*remaining), nil
	}

	// No remainder reported. Either the page held everything — the ordinary
	// case for a namespace with none or one — or the server does not report
	// totals at all, which its continue token gives away: there is more, and
	// it will not say how much.
	if list.GetContinue() != "" {
		return 0, fmt.Errorf("%s: %w", op, ports.ErrCountUnavailable)
	}

	return len(list.Items), nil
}
