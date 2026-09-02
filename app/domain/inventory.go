package domain

import (
	"cmp"
	"slices"
)

// This file answers "what is in this namespace", which is the first question a
// namespace panel exists for and the one Kubernetes has no endpoint for. There
// is no API that reports a namespace's contents: the answer is assembled kind
// by kind, and the shape of that answer — which kinds are worth counting, what
// an unreadable count means, and what a total is a total OF — is a decision
// rather than a lookup, which is why it lives here.

// ResourceCount is how many objects of one kind a namespace holds.
type ResourceCount struct {
	// Kind is what was counted, carried whole so a caller can navigate to it.
	Kind ResourceKind
	// Count is how many exist. Meaningless unless Known.
	Count int
	// Unreadable says why the count is unknown, when it is.
	//
	// AN UNKNOWN COUNT IS NOT ZERO. An account routinely holds `list pods`
	// and not `list secrets`, and rendering that refusal as "Secrets 0" tells
	// an operator a namespace is empty when it may be full. Every consumer of
	// this type has to deal with the difference, which is why it is a field
	// rather than a zero value.
	Unreadable string
}

// Known reports whether the count is a number rather than a refusal.
func (c ResourceCount) Known() bool { return c.Unreadable == "" }

// NamespaceInventory is what one namespace holds, kind by kind.
type NamespaceInventory struct {
	// Namespace is what was counted.
	Namespace NamespaceName
	// Counts holds the kinds worth showing: those with at least one object,
	// and those that could not be read. Ordered by count, largest first.
	Counts []ResourceCount
	// Empty is how many kinds were counted and found to hold nothing. Kept as
	// a number rather than as rows, because a panel listing fourteen zeroes
	// buries the four counts that matter — but "and 14 kinds hold nothing" is
	// the difference between a namespace that is empty and one that was not
	// fully looked at.
	Empty int
	// Total is the sum of the known counts.
	//
	// A TOTAL OF WHAT WAS COUNTED, not of everything in the namespace: custom
	// resources are excluded (see CountableKinds), and so is anything an
	// account may not list. Consumers must say so rather than presenting this
	// as the size of the namespace.
	Total int
	// Unreadable is how many kinds were refused, so a consumer can qualify
	// the total without walking Counts.
	Unreadable int
}

// IsEmpty reports whether nothing at all was found and nothing was refused —
// the state of a namespace that genuinely holds none of the kinds counted.
func (i NamespaceInventory) IsEmpty() bool {
	return i.Total == 0 && i.Unreadable == 0
}

// CountableKinds selects the kinds a namespace inventory counts.
//
// Three exclusions, each for its own reason.
//
// CLUSTER-SCOPED KINDS do not live in a namespace, so counting them per
// namespace would report the same number under every one.
//
// EVENTS expire after roughly an hour. Their count measures how recently
// something happened rather than what the namespace holds, and on a busy
// namespace it is larger than everything else combined — so including it
// would make the total useless as a size.
//
// CUSTOM RESOURCES are excluded because their number is unbounded: a cluster
// with two hundred CRDs would turn opening one panel into two hundred
// requests. This is the exclusion that costs something real — a namespace
// full of custom resources is under-reported — so consumers must say that the
// total counts built-in kinds rather than everything.
func CountableKinds(kinds []ResourceKind) []ResourceKind {
	countable := make([]ResourceKind, 0, len(kinds))
	for _, kind := range kinds {
		switch {
		case !kind.Namespaced:
		case kind.Category == CategoryCustomResources:
		case kind.Kind == "Event":
		default:
			countable = append(countable, kind)
		}
	}
	return countable
}

// NewNamespaceInventory assembles counts into the answer a panel renders.
//
// Ordered by count descending, because the question is what a namespace is
// mostly made of and the answer is at the top of that order. Ties break on
// title so the list is stable between refreshes rather than reshuffling
// whenever two kinds hold the same number.
//
// Kinds that could not be read sort last and are kept, unlike empty ones: an
// operator needs to know the picture is partial, and where.
func NewNamespaceInventory(namespace NamespaceName, counts []ResourceCount) NamespaceInventory {
	inventory := NamespaceInventory{Namespace: namespace}

	for _, count := range counts {
		switch {
		case !count.Known():
			inventory.Unreadable++
			inventory.Counts = append(inventory.Counts, count)
		case count.Count == 0:
			inventory.Empty++
		default:
			inventory.Total += count.Count
			inventory.Counts = append(inventory.Counts, count)
		}
	}

	slices.SortStableFunc(inventory.Counts, func(a, b ResourceCount) int {
		if a.Known() != b.Known() {
			if a.Known() {
				return -1
			}
			return 1
		}
		if byCount := cmp.Compare(b.Count, a.Count); byCount != 0 {
			return byCount
		}
		return cmp.Compare(a.Kind.Title, b.Kind.Title)
	})

	return inventory
}
