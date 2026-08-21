package domain

import "sort"

// KubeconfigMerge describes what adding a kubeconfig would change — or, after
// the fact, what it did.
//
// The same value answers both questions so that what an operator is shown
// before confirming is produced by the same code that performs the write.
// A preview computed one way and a write performed another is how a dialog
// ends up promising something the file does not receive.
type KubeconfigMerge struct {
	// Added names the contexts that would be, or were, added. Sorted.
	Added []string
	// Conflicts names contexts the kubeconfig already defines. Sorted.
	//
	// A conflict is refused rather than merged. kubectl's own merge rules let
	// a later file win, but silently replacing the credentials of a context
	// that works today is not something to do on a paste — the operator can
	// rename the incoming context and try again.
	Conflicts []string
	// Path is the file that was, or would be, written.
	Path string
}

// NewKubeconfigMerge returns a merge with its name lists sorted, so a preview
// and the summary that follows it never disagree about order.
func NewKubeconfigMerge(added, conflicts []string, path string) KubeconfigMerge {
	sort.Strings(added)
	sort.Strings(conflicts)
	return KubeconfigMerge{Added: added, Conflicts: conflicts, Path: path}
}

// HasConflicts reports whether anything would be overwritten.
func (m KubeconfigMerge) HasConflicts() bool { return len(m.Conflicts) > 0 }

// IsEmpty reports whether there is nothing to add — a kubeconfig that parses
// but describes no contexts, which is a paste that went wrong rather than a
// change worth making.
func (m KubeconfigMerge) IsEmpty() bool { return len(m.Added) == 0 && len(m.Conflicts) == 0 }
