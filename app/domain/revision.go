package domain

import "time"

// Revision is one recorded version of a Deployment, StatefulSet or
// DaemonSet's pod template — the same history `kubectl rollout history`
// reads and `kubectl rollout undo --to-revision` rolls back to.
//
// A Deployment's revisions are its ReplicaSets, a StatefulSet's or
// DaemonSet's are its ControllerRevisions — both resolved by ownerReference
// and never by label selector, the same rule ListPodsForWorkload follows
// (see CLAUDE.md, "A pod belongs to the controller that OWNS it"), because a
// selector can be shared by an unrelated object wearing the same labels.
type Revision struct {
	// Number is the revision number Kubernetes itself assigned — the
	// `deployment.kubernetes.io/revision` annotation for a ReplicaSet, or a
	// ControllerRevision's own Revision field for the other two kinds.
	Number int64
	// Name is the owning ReplicaSet's or ControllerRevision's own name —
	// what a rollback actually reads its template from.
	Name string
	// Created is when this revision's object was created.
	Created time.Time
	// Current marks the revision presently in use. Both controllers that
	// carry a history reuse and re-number an existing object rather than
	// creating a new one when a rollback's target template already exists,
	// so the object with the HIGHEST revision number is always the active
	// one — in steady state and immediately after a rollback alike. See the
	// adapter for why this holds for all three kinds without reading kind-
	// specific status fields that not every kind carries.
	Current bool
	// Replicas is how many pods this revision currently has. Set only for a
	// Deployment's ReplicaSet; always zero for a StatefulSet or DaemonSet
	// revision, because a ControllerRevision is a stored patch, not a scaled
	// object with pods of its own.
	Replicas int32
	// Images are the pod template's container images — init containers
	// included — so a history row says what changed without opening the
	// diff.
	Images []string
	// ChangeCause is the `kubernetes.io/change-cause` annotation when the
	// object that produced this revision carried one — kubectl's deprecated
	// `--record` flag writes it, and an operator may set it by hand. Empty
	// when absent, which is the common case.
	ChangeCause string
	// TemplateYAML is the pod template this revision would roll back to,
	// serialised as YAML for the diff view — produced in the adapter FROM
	// THE OBJECT'S OWN MANIFEST (the ReplicaSet's spec.template, or the
	// ControllerRevision's stored patch data), never from the watch store,
	// which strips a ReplicaSet's template to its images. See CLAUDE.md,
	// "Anything rendering a pod TEMPLATE must read the object's own
	// manifest, never the watch store."
	TemplateYAML string
}

// RollbackOutcome reports what RollbackWorkload actually did.
type RollbackOutcome struct {
	// ToRevision is the revision number rolled back to.
	ToRevision int64
	// DryRun reports whether this outcome came from a server-side dry run —
	// nothing was persisted when it is true, mirroring ApplyOutcome.DryRun.
	DryRun bool
}
