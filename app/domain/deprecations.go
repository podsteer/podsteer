package domain

import (
	"fmt"
	"strings"
)

// A hand-compiled table of Kubernetes API removals, in the same spirit as
// release.go's support-window table: it goes stale by construction, so the
// rule throughout is to say nothing about a group/version this build does not
// know rather than something wrong.
//
// Source: the Kubernetes deprecated API migration guide —
// https://kubernetes.io/docs/reference/using-api/deprecation-guide/ — which
// states an exact "no longer served as of vX.Y" for every entry below, and
// names the version its replacement has been "available since". That second
// figure is used here as DeprecatedIn: Kubernetes' own convention is that an
// old API version is superseded the moment its replacement exists, even
// though the old one keeps being served for a further release or several.
// The guide does not, however, give a removal-independent "first deprecated"
// date in any structured way, so DeprecatedIn is left blank ("") for the
// handful of entries below where no "available since" sentence names one —
// never guessed, never backfilled from memory.
//
// THREE THINGS DELIBERATELY LEFT OUT, because the guide could not confirm
// them and the rule here is exactly release.go's: leave out rather than
// guess.
//   - resource.k8s.io (Dynamic Resource Allocation, currently v1alpha3) does
//     not appear on the guide at all. Alpha API versions are not covered by
//     the deprecation policy the guide documents — they can be changed or
//     removed without any notice period — so there is no removal version to
//     cite, and inventing one would be exactly the kind of table nobody
//     checks that release.go's own comment warns about.
//   - apiserverinternal.k8s.io/v1alpha1 StorageVersion is likewise absent
//     from the guide, for the same reason: it is still alpha.
//   - authentication.k8s.io/v1beta1 SelfSubjectReview is absent too. The
//     guide's v1.32 section — the newest one it has — covers only the
//     flow-control removal below; nothing on the page discusses an
//     authentication resource being removed.
//
// Confirmed through Kubernetes 1.32, the newest minor with a removal the
// guide documents as of this table's compilation. A future minor's removals
// are simply not in the table yet, which SupportFor's own comment already
// establishes is the honest state to be in rather than a gap to paper over.
var deprecations = []Deprecation{
	// --- v1.22 ---
	{
		Group: "extensions", Version: "v1beta1", Kind: "Ingress", Resource: "ingresses",
		DeprecatedIn: "1.19", RemovedIn: "1.22", ReplacedBy: "networking.k8s.io/v1",
	},
	{
		Group: "networking.k8s.io", Version: "v1beta1", Kind: "Ingress", Resource: "ingresses",
		DeprecatedIn: "1.19", RemovedIn: "1.22", ReplacedBy: "networking.k8s.io/v1",
	},
	{
		Group: "admissionregistration.k8s.io", Version: "v1beta1", Kind: "MutatingWebhookConfiguration",
		Resource:     "mutatingwebhookconfigurations",
		DeprecatedIn: "1.16", RemovedIn: "1.22", ReplacedBy: "admissionregistration.k8s.io/v1",
	},
	{
		Group: "admissionregistration.k8s.io", Version: "v1beta1", Kind: "ValidatingWebhookConfiguration",
		Resource:     "validatingwebhookconfigurations",
		DeprecatedIn: "1.16", RemovedIn: "1.22", ReplacedBy: "admissionregistration.k8s.io/v1",
	},

	// --- v1.25 ---
	{
		// No direct API successor: Pod Security Admission is a built-in
		// admission controller configured by namespace labels, not a
		// resource an operator migrates a manifest to. The guide states no
		// deprecated-in date for this one either — only that the admission
		// controller itself "will be removed" alongside the API.
		Group: "policy", Version: "v1beta1", Kind: "PodSecurityPolicy", Resource: "podsecuritypolicies",
		DeprecatedIn: "", RemovedIn: "1.25",
		ReplacedBy: "Pod Security Admission (no direct API replacement)",
	},
	{
		Group: "policy", Version: "v1beta1", Kind: "PodDisruptionBudget", Resource: "poddisruptionbudgets",
		DeprecatedIn: "1.21", RemovedIn: "1.25", ReplacedBy: "policy/v1",
	},
	{
		Group: "autoscaling", Version: "v2beta1", Kind: "HorizontalPodAutoscaler", Resource: "horizontalpodautoscalers",
		DeprecatedIn: "1.23", RemovedIn: "1.25", ReplacedBy: "autoscaling/v2",
	},
	{
		Group: "batch", Version: "v1beta1", Kind: "CronJob", Resource: "cronjobs",
		DeprecatedIn: "1.21", RemovedIn: "1.25", ReplacedBy: "batch/v1",
	},
	{
		Group: "discovery.k8s.io", Version: "v1beta1", Kind: "EndpointSlice", Resource: "endpointslices",
		DeprecatedIn: "1.21", RemovedIn: "1.25", ReplacedBy: "discovery.k8s.io/v1",
	},
	{
		Group: "node.k8s.io", Version: "v1beta1", Kind: "RuntimeClass", Resource: "runtimeclasses",
		DeprecatedIn: "1.20", RemovedIn: "1.25", ReplacedBy: "node.k8s.io/v1",
	},
	{
		Group: "events.k8s.io", Version: "v1beta1", Kind: "Event", Resource: "events",
		DeprecatedIn: "1.19", RemovedIn: "1.25", ReplacedBy: "events.k8s.io/v1",
	},

	// --- v1.26 ---
	{
		Group: "autoscaling", Version: "v2beta2", Kind: "HorizontalPodAutoscaler", Resource: "horizontalpodautoscalers",
		DeprecatedIn: "1.23", RemovedIn: "1.26", ReplacedBy: "autoscaling/v2",
	},
	{
		// The guide's only migration target for this one is v1beta2 — v1
		// did not exist yet when v1beta1 stopped being served. See the
		// v1beta2 and v1beta3 entries below for where that chain ends.
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta1", Kind: "FlowSchema", Resource: "flowschemas",
		DeprecatedIn: "", RemovedIn: "1.26", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1beta2",
	},
	{
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta1", Kind: "PriorityLevelConfiguration",
		Resource:     "prioritylevelconfigurations",
		DeprecatedIn: "", RemovedIn: "1.26", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1beta2",
	},

	// --- v1.27 ---
	{
		Group: "storage.k8s.io", Version: "v1beta1", Kind: "CSIStorageCapacity", Resource: "csistoragecapacities",
		DeprecatedIn: "1.24", RemovedIn: "1.27", ReplacedBy: "storage.k8s.io/v1",
	},

	// --- v1.29 ---
	{
		// The guide offers both v1beta3 (available since 1.26) and v1
		// (available since 1.29) as migration targets here; v1 is named
		// because it is the one nothing else in this chain will remove.
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta2", Kind: "FlowSchema", Resource: "flowschemas",
		DeprecatedIn: "1.26", RemovedIn: "1.29", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1",
	},
	{
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta2", Kind: "PriorityLevelConfiguration",
		Resource:     "prioritylevelconfigurations",
		DeprecatedIn: "1.26", RemovedIn: "1.29", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1",
	},

	// --- v1.32 ---
	{
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta3", Kind: "FlowSchema", Resource: "flowschemas",
		DeprecatedIn: "1.29", RemovedIn: "1.32", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1",
	},
	{
		Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta3", Kind: "PriorityLevelConfiguration",
		Resource:     "prioritylevelconfigurations",
		DeprecatedIn: "1.29", RemovedIn: "1.32", ReplacedBy: "flowcontrol.apiserver.k8s.io/v1",
	},
}

// Deprecation is one API version's entry in the removal table above.
type Deprecation struct {
	// Group, Version, Resource identify the API exactly as ResourceKind
	// does, so a served kind is matched against this table by Group,
	// Version and Resource together — see ResourceKind.ID(). Kind is
	// carried too, for display; it is never part of the match, because
	// Group+Version+Resource is already the API server's own identity for
	// a resource and adding Kind to the comparison could only narrow it by
	// accident.
	Group, Version, Kind, Resource string
	// DeprecatedIn is the minor the guide's "available since" note names
	// for this entry's replacement — see the file comment for why that
	// stands in for a deprecation date. Blank when the guide does not
	// state one.
	DeprecatedIn string
	// RemovedIn is the minor this API version stopped being served, per
	// the guide.
	RemovedIn string
	// ReplacedBy names what the guide says to migrate to: a group/version
	// for a direct API successor, or a sentence when there is none.
	ReplacedBy string
}

// ResourceKind returns the served kind this entry names, for listing it in
// the navigator or counting it through the same ports every other kind is
// counted through.
//
// Only Group, Version, Resource and Kind are carried — nothing else has a
// meaning for a table entry that is not itself a full catalog kind (it is
// never Namespaced, categorised, or Rich).
func (d Deprecation) ResourceKind() ResourceKind {
	return ResourceKind{Group: d.Group, Version: d.Version, Resource: d.Resource, Kind: d.Kind}
}

// APIGroupVersion is one API version a cluster serves, as discovery reports
// it — the honest input to UpgradeImpact, unlike Catalog.Kinds, which only
// ever names the CURRENT version PodSteer targets for a kind and therefore
// can never contain a deprecated one.
type APIGroupVersion struct {
	Group, Version string
}

// String renders the group/version the way the API server does: "v1" for the
// core group, "group/version" otherwise.
func (g APIGroupVersion) String() string {
	if g.Group == "" {
		return g.Version
	}
	return g.Group + "/" + g.Version
}

// APIWriter is one manager that last wrote one object through a deprecated
// API version, per that object's own metadata.managedFields.
//
// An OBJECT does not use an API version — a WRITER does. Kubernetes stores
// one copy of an object and serves it through every version the API server
// offers, so counting objects under a deprecated version counts precisely
// what the replacement version would also count. What actually breaks at
// removal is whatever keeps WRITING through the old version: a Helm release
// that has not been upgraded, a controller still built against it, a
// kubectl invocation with the version pinned. managedFields is where
// Kubernetes already records that, per field, per manager.
type APIWriter struct {
	Manager   string
	Namespace NamespaceName
	Name      string
	// SelfManaged reports whether the object carries
	// apf.kubernetes.io/autoupdate-spec: "true" — the API server's own mark
	// on FlowSchemas and PriorityLevelConfigurations it bootstraps and keeps
	// current itself. Set by the adapter from the object's own annotations;
	// what to DO with it (exclude it from the verdict) is a domain decision,
	// made in operatorWriters below rather than by filtering it out here, so
	// the exclusion is a tested rule rather than a silent drop.
	SelfManaged bool
}

// APIUsage is what a scan of one deprecated, served resource found.
type APIUsage struct {
	// Writers is deduplicated on (Manager, Namespace, Name), in list order.
	Writers []APIWriter
	// Scanned is how many objects were inspected, bounded by the caller's
	// scan limit.
	Scanned int
	// Truncated reports whether the scan stopped at its limit with objects
	// still left to inspect.
	Truncated bool
}

// UpgradeCandidates returns the table entries whose group/version the
// cluster currently serves, in table order.
//
// Exported for the application layer: deciding which served group/versions
// are worth checking for writers is a question about this table, so it
// belongs here rather than being duplicated as a second copy of the match
// logic one layer up. The overview service calls this to bound its scanning
// to exactly the entries UpgradeImpact could ever flag.
func UpgradeCandidates(served []APIGroupVersion) []Deprecation {
	set := make(map[APIGroupVersion]bool, len(served))
	for _, gv := range served {
		set[gv] = true
	}

	candidates := make([]Deprecation, 0, 4)
	for _, dep := range deprecations {
		if set[APIGroupVersion{Group: dep.Group, Version: dep.Version}] {
			candidates = append(candidates, dep)
		}
	}
	return candidates
}

// UpgradeImpact reports which of a cluster's currently served APIs would stop
// being served at or before target, and which are already deprecated but
// would survive it regardless.
//
// served is every group/version discovery reports the cluster serves — the
// same input UpgradeCandidates takes. A custom resource's group is never one
// this table names, Kubernetes reserves its own API groups, so a CRD can
// never match an entry here; nothing needs to special-case
// CategoryCustomResources for that to hold.
//
// current and target are compared as minors only, exactly as SupportFor
// compares them, and for the same reason: an unparseable version, or a
// target this table cannot place relative to current, yields nothing rather
// than a wrong verdict. A target older than current is rejected outright —
// it is not an upgrade, and comparing against it would describe moving
// backwards as removing something.
//
// usage reports, for each candidate entry actually checked, who is still
// writing through it — gathered by the caller from managedFields (see
// APIUsage), never from a count of objects. It is keyed by
// Deprecation.ResourceKind().ID(). A key absent from usage is read as "not
// checked" rather than "no writers": the distinction only changes whether a
// finding states a fact or says it could not be established, never whether
// the finding appears.
func UpgradeImpact(served []APIGroupVersion, current, target ServerVersion, usage map[string]APIUsage) []Finding {
	currentMinor, currentOK := minorOf(current.GitVersion)
	targetMinor, targetOK := minorOf(target.GitVersion)
	if !currentOK || !targetOK {
		return nil
	}
	if compareMinor(targetMinor, currentMinor) < 0 {
		return nil
	}

	findings := make([]Finding, 0, 4)
	for _, dep := range UpgradeCandidates(served) {
		// Already gone as of the CURRENT version is not something this
		// upgrade causes — it is a fact about right now. Reporting it here
		// would tell an operator that upgrading breaks an API that is
		// already broken, which sends them looking for a connection between
		// the two that does not exist.
		if compareMinor(dep.RemovedIn, currentMinor) <= 0 {
			continue
		}

		use, checked := usage[dep.ResourceKind().ID()]

		switch {
		case compareMinor(dep.RemovedIn, targetMinor) <= 0:
			findings = append(findings, removalFinding(dep, targetMinor, use, checked))

		case dep.DeprecatedIn != "" && compareMinor(dep.DeprecatedIn, currentMinor) <= 0:
			findings = append(findings, stillServedFinding(dep, targetMinor, use, checked))
		}
	}

	return findings
}

// operatorWriters splits writers into the ones the verdict should judge on
// and a count of the ones it should not.
//
// A writer whose object carries the API server's own autoupdate-spec
// annotation is the control plane maintaining its OWN default FlowSchemas
// and PriorityLevelConfigurations: on a cluster upgraded from 1.28 or
// earlier those objects keep a managedFields entry from the OLD producer
// even though the running producer already writes through the current
// version and will rewrite them on the next upgrade. Reporting that as a
// writer to migrate would mark every long-lived cluster critical for an
// object nobody actually wrote — the adapter reports the fact (it is a
// quotation, see APIWriter.SelfManaged), and this is the verdict, argued
// over in a test.
func operatorWriters(writers []APIWriter) (own []APIWriter, selfManaged int) {
	own = make([]APIWriter, 0, len(writers))
	for _, w := range writers {
		if w.SelfManaged {
			selfManaged++
			continue
		}
		own = append(own, w)
	}
	return own, selfManaged
}

// selfManagedNote appends the sentence naming self-managed writers that were
// excluded from the verdict, when there were any.
func selfManagedNote(selfManaged int) string {
	if selfManaged == 0 {
		return ""
	}
	return fmt.Sprintf(" %s maintained by the control plane itself still carry an older entry; "+
		"the upgrade rewrites those and they need nothing.", plural(selfManaged, "object", "objects"))
}

// removalFinding reports an API the target version no longer serves at all.
func removalFinding(dep Deprecation, targetMinor string, use APIUsage, checked bool) Finding {
	gv := dep.ResourceKind().GroupVersion()
	own, selfManaged := operatorWriters(use.Writers)
	note := selfManagedNote(selfManaged)

	switch {
	case checked && len(own) > 0:
		summary := fmt.Sprintf("%s last written through %s, which %s no longer serves — those writers fail after the upgrade.",
			plural(len(own), "object", "objects"), gv, dep.RemovedIn)
		if use.Truncated {
			summary += fmt.Sprintf(" First %d objects checked.", use.Scanned)
		}
		summary += note

		return Finding{
			ID:       "upgrade:" + dep.ResourceKind().ID(),
			Severity: SeverityCritical,
			Category: CategoryFindingUpgrade,
			Title:    fmt.Sprintf("%s: %s removed in Kubernetes %s", dep.Kind, gv, dep.RemovedIn),
			Summary:  summary,
			Advice: fmt.Sprintf(
				"Update what still writes %s through %s — %s — to %s before upgrading to %s. "+
					"Existing objects survive the upgrade; the clients still using the old version do not.",
				dep.Kind, gv, strings.Join(distinctManagers(own), ", "), dep.ReplacedBy, targetMinor),
			Subjects: writerSubjects(dep.Kind, own),
			Count:    len(own),
		}

	case checked:
		return Finding{
			ID:       "upgrade:" + dep.ResourceKind().ID(),
			Severity: SeverityWarning,
			Category: CategoryFindingUpgrade,
			Title:    fmt.Sprintf("%s: %s removed in Kubernetes %s", dep.Kind, gv, dep.RemovedIn),
			Summary: fmt.Sprintf("%s is still served and stops being served in %s; nothing was found written through it (%s checked).%s",
				gv, dep.RemovedIn, plural(use.Scanned, "object", "objects"), note),
			Advice: fmt.Sprintf(
				"Check anything that still reads %s through %s — kubectl invocations, scripts, dashboards — and move it to %s before upgrading to %s.",
				dep.Kind, gv, dep.ReplacedBy, targetMinor),
			Subjects: []Subject{{Kind: dep.Kind, Name: gv, Detail: "no writers found"}},
		}

	default:
		return Finding{
			ID:       "upgrade:" + dep.ResourceKind().ID(),
			Severity: SeverityWarning,
			Category: CategoryFindingUpgrade,
			Title:    fmt.Sprintf("%s: %s removed in Kubernetes %s", dep.Kind, gv, dep.RemovedIn),
			Summary: fmt.Sprintf("%s is still served and stops being served in %s; whether anything still writes through it could not be checked.",
				gv, dep.RemovedIn),
			Advice: fmt.Sprintf(
				"Check anything that still reads %s through %s — kubectl invocations, scripts, dashboards — and move it to %s before upgrading to %s. "+
					"PodSteer could not list %s to check for writers.",
				dep.Kind, gv, dep.ReplacedBy, targetMinor, dep.Resource),
			Subjects: []Subject{{Kind: dep.Kind, Name: gv, Detail: "usage not checked"}},
		}
	}
}

// stillServedFinding reports an API that is already deprecated but that the
// target version keeps serving regardless — nothing breaks at target, but
// the clock the table can see is already running.
func stillServedFinding(dep Deprecation, targetMinor string, use APIUsage, checked bool) Finding {
	gv := dep.ResourceKind().GroupVersion()
	own, selfManaged := operatorWriters(use.Writers)
	note := selfManagedNote(selfManaged)

	var extent string
	var subjects []Subject
	switch {
	case checked && len(own) > 0:
		extent = fmt.Sprintf("%s still use it", plural(len(own), "writer", "writers"))
		subjects = writerSubjects(dep.Kind, own)
	case checked:
		extent = "nothing was found written through it"
		subjects = []Subject{{Kind: dep.Kind, Name: gv, Detail: "no writers found"}}
	default:
		extent = "usage not checked"
		subjects = []Subject{{Kind: dep.Kind, Name: gv, Detail: "usage not checked"}}
	}

	return Finding{
		ID:       "upgrade:" + dep.ResourceKind().ID(),
		Severity: SeverityInfo,
		Category: CategoryFindingUpgrade,
		Title:    fmt.Sprintf("%s: %s deprecated, still served at %s", dep.Kind, gv, targetMinor),
		Summary: fmt.Sprintf("%s has been superseded by %s since %s and is still served at %s; %s.%s",
			gv, dep.ReplacedBy, dep.DeprecatedIn, targetMinor, extent, note),
		Advice: fmt.Sprintf("Migrate to %s on your own schedule, before the upgrade that removes it (%s).",
			dep.ReplacedBy, dep.RemovedIn),
		Subjects: subjects,
		Count:    len(own),
		// KindID is deliberately left empty: the navigator has no entry for
		// a deprecated version any more than removalFinding's does — this
		// group/version is still served, but nothing here names a LIVE kind
		// entry to click through to, so a dead click-through is worse than
		// none.
	}
}

// writerSubjects turns writers into subjects, capped at maxSubjects so one
// widely applied deprecated version cannot produce a payload with a thousand
// entries in it.
func writerSubjects(kind string, writers []APIWriter) []Subject {
	n := len(writers)
	if n > maxSubjects {
		n = maxSubjects
	}
	subjects := make([]Subject, n)
	for i := range n {
		w := writers[i]
		subjects[i] = Subject{Kind: kind, Namespace: w.Namespace, Name: w.Name, Detail: "last written by " + w.Manager}
	}
	return subjects
}

// distinctManagers returns the managers named across writers, deduplicated
// and in first-seen order, capped at five — a finding names who to go find,
// not an exhaustive audit log.
func distinctManagers(writers []APIWriter) []string {
	seen := make(map[string]bool, len(writers))
	managers := make([]string, 0, 5)
	for _, w := range writers {
		if seen[w.Manager] {
			continue
		}
		seen[w.Manager] = true
		managers = append(managers, w.Manager)
		if len(managers) == 5 {
			break
		}
	}
	return managers
}
