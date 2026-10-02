package application

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// scopeReadConcurrency bounds the per-namespace reads of one list. A named
// scope reads one namespace per request when it is short (see
// domain.NamespaceListCap) and when a cluster-wide read was refused, where it
// can be as long as the account's own namespace list.
const scopeReadConcurrency = 8

// readScoped reads one kind of object over a scope of namespaces.
//
// ONE CLUSTER-WIDE LIST when the scope is All or longer than
// domain.NamespaceListCap — then filtered to the scope, because a request
// for six namespaces out of two hundred is cheaper as one list than six
// only up to a point, and that point is the cap. Otherwise ONE LIST PER
// NAMESPACE, which also covers an account bound to a few namespaces that may
// not list across the cluster: a cluster-wide list REFUSED for a named scope
// falls back to per-namespace lists. For All the refusal is the answer.
//
// A PER-NAMESPACE FAILURE FAILS THE LIST, naming the namespace. Lists do not
// degrade: a table of two namespaces that silently shows one is a wrong
// answer with a plausible shape, which is worse than an error.
func readScoped[T any](
	ctx context.Context,
	scope domain.NamespaceScope,
	namespaceOf func(T) domain.NamespaceName,
	list func(ctx context.Context, namespace domain.NamespaceName) ([]T, error),
) ([]T, error) {
	// The zero scope names nothing and is not All; reading it as All beats
	// a silently empty list.
	if scope.Everything() {
		return list(ctx, domain.NamespaceAll)
	}

	if scope.ListsClusterWide() {
		items, err := list(ctx, domain.NamespaceAll)
		if err == nil {
			kept := items[:0:0]
			for _, item := range items {
				if scope.Includes(namespaceOf(item)) {
					kept = append(kept, item)
				}
			}
			return kept, nil
		}
		if !errors.Is(err, ports.ErrForbidden) {
			return nil, err
		}
	}

	parts, err := perNamespace(ctx, scope, list)
	if err != nil {
		return nil, err
	}

	var out []T
	for _, part := range parts {
		out = append(out, part...)
	}
	return out, nil
}

// perNamespace runs one read per namespace of a named scope, concurrently,
// and returns the answers in scope order. The first failure in scope order
// is returned, naming its namespace. Every goroutine ends when its read
// returns, and the group waits for all of them.
func perNamespace[T any](
	ctx context.Context,
	scope domain.NamespaceScope,
	read func(ctx context.Context, namespace domain.NamespaceName) (T, error),
) ([]T, error) {
	if len(scope.Namespaces) == 1 {
		// One namespace is the old single-namespace read: its error text
		// is the caller's own, unprefixed.
		one, err := read(ctx, scope.Namespaces[0])
		return []T{one}, err
	}

	results := make([]T, len(scope.Namespaces))
	errs := make([]error, len(scope.Namespaces))

	var group errgroup.Group
	group.SetLimit(scopeReadConcurrency)
	for i, namespace := range scope.Namespaces {
		group.Go(func() error {
			results[i], errs[i] = read(ctx, namespace)
			return nil
		})
	}
	_ = group.Wait()

	for i, err := range errs {
		if err != nil {
			return nil, fmt.Errorf("listing in %q: %w", scope.Namespaces[i], err)
		}
	}
	return results, nil
}
