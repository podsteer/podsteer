package domain

import (
	"strings"
)

// Who owns a field of an object, and what it means to take it from them.
//
// SERVER-SIDE APPLY MAKES OWNERSHIP EXPLICIT. Every field of every object
// carries the name of the manager that set it, and an apply that would change
// a field somebody else owns is REFUSED with the owner named rather than
// silently winning. That refusal is the useful part: it is the cluster's own
// ledger answering "who put this here", which is a better answer than any
// annotation-based guess — see gitops.ts, which infers from labels a
// controller happened to write, where this is what the controller actually
// did.
//
// PARSING IS QUOTATION, CLASSIFICATION IS VERDICT, and they are separate
// functions for that reason. ParseFieldConflict lifts the server's own words
// out of a status cause without interpreting them; ClassifyManager decides
// what kind of thing the manager is, which is a judgement this package owns
// and a table somebody has to maintain.

// ManagerKind groups field managers by what overriding one would mean.
//
// The kinds exist because the SENTENCE differs, not because the mechanism
// does: taking a field from kubectl takes it from a person who can be asked,
// and taking one from Argo CD takes it from a controller that will put it
// straight back. Offering both as "Take ownership" would make one of the two
// a lie.
type ManagerKind string

const (
	// ManagerPodSteer is PodSteer's own earlier writes.
	ManagerPodSteer ManagerKind = "podsteer"
	// ManagerGitOps is a continuous-reconciliation controller. Overriding one
	// is not durable: it reverts on the next sync.
	ManagerGitOps ManagerKind = "gitops"
	// ManagerKubectl is a person at a terminal.
	ManagerKubectl ManagerKind = "kubectl"
	// ManagerControlPlane is Kubernetes itself — an HPA writing replicas, the
	// scheduler writing nodeName. Overriding one is undone by the controller
	// that owns it, usually within seconds.
	ManagerControlPlane ManagerKind = "control-plane"
	// ManagerUnknown is anything this table does not recognise. It produces
	// the neutral sentence and the manager's own name, which is better than a
	// guess about somebody else's operator.
	ManagerUnknown ManagerKind = "unknown"
)

// knownManagers maps a field manager's name to what kind of thing it is.
//
// HAND-COMPILED AND STALE BY CONSTRUCTION, exactly like the deprecation and
// release tables beside it. A manager this does not know produces
// ManagerUnknown and the server's own message, never a guess: an operator's
// own controller is far more likely to be here than any name we could
// enumerate, and inventing a category for it would put words in its mouth.
//
// Matched on the whole name and on a prefix, because kubectl brands its
// subcommands — kubectl-edit, kubectl-scale, kubectl-rollout — and Flux names
// its controllers by function.
var knownManagers = []struct {
	prefix string
	kind   ManagerKind
}{
	{"podsteer", ManagerPodSteer},
	{"kubectl", ManagerKubectl},
	{"kube-controller-manager", ManagerControlPlane},
	{"kube-scheduler", ManagerControlPlane},
	{"kube-apiserver", ManagerControlPlane},
	{"kubelet", ManagerControlPlane},
	{"cluster-autoscaler", ManagerControlPlane},
	{"argocd-controller", ManagerGitOps},
	{"argocd-application-controller", ManagerGitOps},
	{"argo-cd", ManagerGitOps},
	{"kustomize-controller", ManagerGitOps},
	{"helm-controller", ManagerGitOps},
	{"source-controller", ManagerGitOps},
	{"flux", ManagerGitOps},
}

// ClassifyManager says what kind of thing a field manager is.
func ClassifyManager(name string) ManagerKind {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ManagerUnknown
	}
	for _, known := range knownManagers {
		if name == known.prefix || strings.HasPrefix(name, known.prefix+"-") {
			return known.kind
		}
	}
	return ManagerUnknown
}

// FieldConflict is one field an apply could not change, and who holds it.
type FieldConflict struct {
	// Field is the path as the server wrote it, e.g. ".spec.replicas".
	Field string
	// Manager is the owner's name, lifted out of Message. It is the whole
	// message when the message did not parse — better a long label than a
	// wrong one.
	Manager string
	// Message is the server's cause, verbatim, for the cases where the
	// parse was imperfect and an operator needs to see the original.
	Message string
	// Kind is what ClassifyManager made of Manager.
	Kind ManagerKind
}

// FieldConflicts is a whole refusal.
type FieldConflicts []FieldConflict

// ParseFieldConflict lifts a manager's name out of one status cause.
//
// The server's wording is `conflict with "argocd-controller" using apps/v1`,
// and for a manager whose last write was an Update rather than an Apply it
// adds ` at <timestamp>`. The name is taken from between the first pair of
// double quotes and nothing else is interpreted: a message this does not
// recognise keeps the whole text as the manager, which renders as a long
// label rather than as a confident wrong one.
func ParseFieldConflict(field, message string) FieldConflict {
	conflict := FieldConflict{Field: field, Message: message, Manager: message}

	if opening := strings.Index(message, `"`); opening >= 0 {
		rest := message[opening+1:]
		if closing := strings.Index(rest, `"`); closing > 0 {
			conflict.Manager = rest[:closing]
		}
	}

	conflict.Kind = ClassifyManager(conflict.Manager)
	return conflict
}

// AllOwnedBy reports whether every conflict belongs to one kind of manager.
//
// Used to decide whether a refusal can be resolved without asking — a set
// entirely owned by PodSteer's own earlier writes is the same tool and the
// same intent — so it is deliberately false for an empty set: nothing is not
// "all ours".
func (c FieldConflicts) AllOwnedBy(kind ManagerKind) bool {
	if len(c) == 0 {
		return false
	}
	for _, conflict := range c {
		if conflict.Kind != kind {
			return false
		}
	}
	return true
}

// Managers returns the distinct owners, in the order first seen.
func (c FieldConflicts) Managers() []string {
	seen := make(map[string]bool, len(c))
	names := make([]string, 0, len(c))
	for _, conflict := range c {
		if seen[conflict.Manager] {
			continue
		}
		seen[conflict.Manager] = true
		names = append(names, conflict.Manager)
	}
	return names
}

// CoveredBy reports whether every conflict here was one the operator was
// shown and agreed to.
//
// THE PRECONDITION FOR FORCING, and the reason force is not a retry. A
// dialog naming three managers is a claim about the cluster at the moment it
// was drawn; between reading it and pressing the button a fourth manager can
// take a field, and forcing then would override somebody the operator was
// never shown. So the live set is re-read and checked against what they
// confirmed: anything new, and the write does not happen.
//
// Matched on FIELD AND MANAGER together. The same field changing hands is a
// different fact from the same manager holding it, and confirming "take
// .spec.replicas from argocd-controller" is not consent to take it from
// whoever holds it now.
func (c FieldConflicts) CoveredBy(confirmed FieldConflicts) bool {
	for _, live := range c {
		found := false
		for _, agreed := range confirmed {
			if live.Field == agreed.Field && live.Manager == agreed.Manager {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ApplyOptions are the choices an apply is made with.
type ApplyOptions struct {
	// DryRun asks the server what it would do and store nothing.
	DryRun bool
	// Force takes ownership of every field in Confirmed.
	//
	// NEVER SET WITHOUT Confirmed. A force with nothing confirmed is a retry
	// that wins, which is the shape this refuses to have: it would let an
	// interface turn "the server declined" into "press again" without anybody
	// reading who owns what.
	Force bool
	// Confirmed is the conflict set the operator was shown and agreed to
	// override. Checked against the cluster before the forced write — see
	// CoveredBy.
	Confirmed FieldConflicts
}
