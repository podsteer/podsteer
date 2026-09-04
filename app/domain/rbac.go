package domain

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The RBAC explorer's domain half.
//
// The dividing line here is sharper than anywhere else in this package, and
// it is worth stating before the types: THE API SERVER DECIDES, AND PODSTEER
// ONLY FLAGS. Whether an account may do something is answered by the
// authorization review APIs and quoted verbatim — allowed, denied and the
// reason string, exactly as they came back. Re-implementing that evaluation
// here would mean re-implementing aggregation, wildcards, the binding graph
// and every authorizer in the chain (RBAC is only one of them; a cluster may
// also run Node, ABAC or a webhook), and being subtly wrong about somebody's
// permissions is worse than saying nothing at all.
//
// What DOES live here is the blast-radius assessment: a verdict about what a
// set of rules PERMITS, which is a rule with a threshold and an argument, not
// a quotation. That is the same line pod_assessment.go draws, and it is why
// AssessRole is a pure function with a test per finding.

// ReviewStatus says WHY a review has no answer, when it has none.
//
// Modelled on MetricsStatus, and for the same reason: an absent answer, a
// refused one and a failed one need opposite advice. An account that may not
// create a SelfSubjectAccessReview is told to ask for that permission; a
// cluster too old to serve the review API is told the cluster does not offer
// it; a timeout is told to try again. Collapsing the three into an empty pane
// sends the first person to check their network.
type ReviewStatus string

const (
	// ReviewAnswered means the API server answered the review.
	ReviewAnswered ReviewStatus = "answered"
	// ReviewForbidden means this account may not ask. HTTP 403 — an RBAC
	// question about the question, which is ordinary: creating a
	// SubjectAccessReview for somebody else is a privileged act, and plenty
	// of accounts that can read a cluster cannot perform one.
	ReviewForbidden ReviewStatus = "forbidden"
	// ReviewAbsent means there was nothing to answer with: the cluster
	// serves no such review API, or the role asked about does not exist.
	// Distinct from a refusal because the advice is opposite — there is
	// nobody to ask for a permission that would help.
	ReviewAbsent ReviewStatus = "absent"
	// ReviewFailed means the read failed for some other reason, including
	// the cluster being unreachable.
	ReviewFailed ReviewStatus = "failed"
)

// RoleScope says whether a set of rules is bound to one namespace or to the
// whole cluster.
//
// The same rule reads very differently at the two scopes: `get secrets` in one
// namespace is how half the workloads in a cluster are configured, and the
// same rule on a ClusterRole reaches every Secret every namespace holds.
type RoleScope string

const (
	// RoleScopeNamespace is a Role: its rules apply in its own namespace.
	RoleScopeNamespace RoleScope = "namespace"
	// RoleScopeCluster is a ClusterRole: its rules apply cluster-wide
	// wherever a ClusterRoleBinding grants them.
	RoleScopeCluster RoleScope = "cluster"
)

// clusterAdminRole is the ClusterRole every conformant cluster ships, holding
// `*` on `*` in `*` plus every non-resource URL. Named here because a binding
// to it is the one binding worth flagging on the name alone.
const clusterAdminRole = "cluster-admin"

// wildcard is RBAC's own "everything" token, in a verb, group or resource
// list. Kubernetes gives it no other meaning — it is not a glob — so an exact
// comparison is the whole match.
const wildcard = "*"

// PolicyRule is one rule, quoted from a Role, a ClusterRole or a rules review.
//
// Field for field what rbac.authorization.k8s.io/v1 carries, deliberately: it
// is displayed as the cluster stated it, and anything that reshaped it would
// be answering a question the operator did not ask.
type PolicyRule struct {
	// Verbs are the actions permitted, e.g. get, list, create. "*" is all.
	Verbs []string
	// APIGroups are the groups the resources live in. "" is the core group.
	APIGroups []string
	// Resources are the resource plurals, optionally with a subresource as
	// "pods/log".
	Resources []string
	// ResourceNames narrows the rule to named objects. Empty means every
	// object of the resource.
	ResourceNames []string
	// NonResourceURLs are the URL paths permitted — "/healthz" and the like.
	// They are cluster-scoped by their nature: a URL belongs to the API
	// server, not to a namespace.
	NonResourceURLs []string
}

// RulesReview is what SelfSubjectRulesReview answered for one namespace.
//
// The two lists are the API's own split and are kept apart on screen for the
// reason the API keeps them apart: resource rules are what the account may do
// to objects IN THAT NAMESPACE, and non-resource rules are URL paths, which
// belong to the API server and so are cluster-scoped whichever namespace was
// asked about.
type RulesReview struct {
	// Resource holds the namespaced resource rules.
	Resource []PolicyRule
	// NonResource holds the cluster-scoped non-resource URL rules.
	NonResource []PolicyRule
	// Incomplete is the API server's own warning that it could not enumerate
	// everything — carried because a partial list that does not say so reads
	// as a complete one.
	Incomplete bool
	// IncompleteReason is the evaluation error behind Incomplete, verbatim.
	IncompleteReason string
}

// SubjectRules is the answer to "what can this kubeconfig actually do here".
//
// One request — SelfSubjectRulesReview — and the API server's own answer. It
// is explicitly NOT an authorization decision the way an access review is:
// Kubernetes' own documentation says it is for a user interface to display
// what somebody may do, not for anything to gate on, and this displays it.
type SubjectRules struct {
	// Namespace is the namespace the review was asked about.
	Namespace NamespaceName
	// Status says whether there is an answer, and why not when there is not.
	Status ReviewStatus
	// Refusal is the sentence to show when Status is not ReviewAnswered.
	Refusal string
	// Review is the answer, empty unless Status is ReviewAnswered.
	Review RulesReview
}

// SubjectKind is the kind of account an RBAC binding names.
type SubjectKind string

const (
	// SubjectUser is a user name, as the authenticator presents it.
	SubjectUser SubjectKind = "User"
	// SubjectGroup is a group name.
	SubjectGroup SubjectKind = "Group"
	// SubjectServiceAccount is a ServiceAccount, which unlike the other two
	// is an object in a namespace.
	SubjectServiceAccount SubjectKind = "ServiceAccount"
)

// RBACSubject is one account a binding names, or one an access review asks
// about.
//
// Named RBACSubject rather than Subject because Subject is already the
// overview's "object a finding is about", and the two would be confused on
// sight. A subject NAME IS AN OBJECT NAME: it may be typed into the panel and
// shown, and it is never written to disk — the same commitment SECURITY.md
// makes about every other object name.
type RBACSubject struct {
	// Kind is User, Group or ServiceAccount.
	Kind SubjectKind
	// Name is the account name, or the ServiceAccount's name.
	Name string
	// Namespace is the ServiceAccount's namespace, and empty for the others.
	Namespace NamespaceName
}

// IsZero reports whether no subject was named — which an access review reads
// as "the current account", the difference between a SelfSubjectAccessReview
// and a SubjectAccessReview.
func (s RBACSubject) IsZero() bool { return s.Kind == "" && s.Name == "" }

// RoleRef is what a binding points at.
type RoleRef struct {
	// Kind is "Role" or "ClusterRole".
	Kind string
	// Name is the role's name.
	Name string
}

// RoleTarget names one Role or ClusterRole to inspect.
type RoleTarget struct {
	// Scope decides which of the two kinds is meant.
	Scope RoleScope
	// Namespace holds the Role's namespace, and is ignored for a ClusterRole.
	Namespace NamespaceName
	// Name is the role's name.
	Name string
}

// Kind renders the target as the Kubernetes kind it names.
func (t RoleTarget) Kind() string {
	if t.Scope == RoleScopeCluster {
		return "ClusterRole"
	}
	return "Role"
}

// RoleBindingRef is one RoleBinding or ClusterRoleBinding, with the subjects
// it carries.
type RoleBindingRef struct {
	// Kind is "RoleBinding" or "ClusterRoleBinding".
	Kind string
	// Name is the binding's name.
	Name string
	// Namespace is the RoleBinding's namespace, and empty for a
	// ClusterRoleBinding.
	Namespace NamespaceName
	// RoleRef is the role the binding grants.
	RoleRef RoleRef
	// Subjects are the accounts it grants it to.
	Subjects []RBACSubject
}

// AccessRequest is one "can I" question, in the API's own vocabulary.
type AccessRequest struct {
	// Subject is the account being asked about. The zero value means the
	// current one, which is a different API — see RBACPort.AccessReview.
	Subject RBACSubject
	// Verb is the action, e.g. "create".
	Verb string
	// Group is the API group, empty for the core group.
	Group string
	// Resource is the resource plural, e.g. "pods".
	Resource string
	// Subresource narrows it, e.g. "log" or "exec".
	Subresource string
	// Namespace scopes the question; empty asks at cluster scope.
	Namespace NamespaceName
	// Name narrows the question to one object.
	Name string
}

// AccessOutcome is the API server's answer, unaltered.
//
// Allowed and Denied are BOTH carried because they are not opposites in the
// Kubernetes API: an authorizer that neither allows nor denies leaves both
// false, which means "no opinion" and is a third answer. Rendering that as a
// denial would claim a verdict nothing gave.
type AccessOutcome struct {
	// Allowed is the API server's own `allowed`.
	Allowed bool
	// Denied is the API server's own `denied`.
	Denied bool
	// Reason is the API server's own `reason`, verbatim.
	Reason string
	// EvaluationError is the API server's own `evaluationError`, verbatim:
	// the authorizer hit a problem and its answer may be incomplete.
	EvaluationError string
}

// AccessDecision is one answered — or unanswerable — access review.
type AccessDecision struct {
	// Request is the question, echoed so a result cannot be read against the
	// wrong one after the form has moved on.
	Request AccessRequest
	// Status says whether there is an answer, and why not when there is not.
	Status ReviewStatus
	// Refusal is the sentence to show when Status is not ReviewAnswered.
	Refusal string
	// Outcome is the answer, meaningful only when Status is ReviewAnswered.
	Outcome AccessOutcome
}

// RoleInspection is one Role or ClusterRole read, with what references it and
// what its rules permit.
//
// The role and the bindings carry SEPARATE statuses because they are separate
// reads and fail separately: an account may read a ClusterRole and not list
// ClusterRoleBindings, which is an ordinary shape, and one refusal must not
// blank the half that answered.
type RoleInspection struct {
	// Target is the role inspected.
	Target RoleTarget
	// Status says whether the role itself could be read.
	Status ReviewStatus
	// Refusal is the sentence to show when Status is not ReviewAnswered.
	Refusal string
	// Rules are the role's own rules, empty unless Status is ReviewAnswered.
	Rules []PolicyRule
	// BindingsStatus says whether the bindings could be listed.
	BindingsStatus ReviewStatus
	// BindingsRefusal is the sentence to show when BindingsStatus is not
	// ReviewAnswered.
	BindingsRefusal string
	// Bindings are the bindings that reference Target.
	Bindings []RoleBindingRef
	// Findings are the blast-radius flags, ranked worst first.
	Findings []RBACFinding
}

// RBACFinding is one blast-radius flag.
//
// The same shape as a PodFinding and deliberately not the same type: these
// are about what a set of rules PERMITS rather than about an object's state,
// so they carry an id the UI can key an expanded row on and no subjects.
type RBACFinding struct {
	// ID is stable for the same flag, so re-inspecting a role does not
	// collapse whatever was open.
	ID string
	// Severity ranks the flag.
	Severity Severity
	// Title is the power granted, in a few words.
	Title string
	// Detail says what it permits, in one sentence.
	Detail string
	// Advice says why it matters. A flag without one is trivia.
	Advice string
}

// RoleReview is what AssessRole is given.
type RoleReview struct {
	// Scope is the role's scope, which changes what the same rule permits.
	Scope RoleScope
	// Name is the role's name.
	Name string
	// Rules are its rules.
	Rules []PolicyRule
	// Bindings are the bindings found to reference it, or none when they
	// could not be listed — a refusal produces no binding findings rather
	// than a claim about bindings nobody read.
	Bindings []RoleBindingRef
}

// AssessRole flags what a role's rules and bindings permit.
//
// A PURE FUNCTION of what was read, like AssessPod and NewOverview, so every
// flag can be argued with in a test rather than only observed against a real
// cluster. It says nothing about whether anybody is ALLOWED to do a thing —
// that is the review APIs' answer and is quoted, never derived. It says what
// this rule, if it is reached, would permit.
//
// A ROLE WITH NONE OF THESE PRODUCES NOTHING, and a test asserts it. A panel
// that always has something to say is one people stop reading — the same rule
// pod_assessment.go holds to.
func AssessRole(review RoleReview) []RBACFinding {
	findings := make([]RBACFinding, 0, 4)

	findings = append(findings, wildcardFindings(review)...)
	findings = append(findings, escalationVerbFindings(review)...)
	findings = append(findings, clusterSecretFindings(review)...)
	findings = append(findings, podCreationFindings(review)...)
	findings = append(findings, clusterAdminBindingFindings(review)...)

	// Ranked, not left in the order the rules above happen to run in: a role
	// that can impersonate AND creates pods must lead with impersonation.
	// Stable within a severity, so the order above still breaks ties.
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].Severity.rank() > findings[j].Severity.rank()
	})

	return findings
}

// wildcardWorth ranks a wildcard by the scope it is granted at.
//
// The SAME token means different things at the two scopes, so the severity
// has to: `*` verbs on ConfigMaps in one namespace is what a team's own admin
// role looks like, and the identical rule on a ClusterRole reaches every
// namespace in the cluster including kube-system.
func wildcardWorth(scope RoleScope) Severity {
	if scope == RoleScopeCluster {
		return SeverityCritical
	}
	return SeverityWarning
}

// scopeWords renders the reach of a rule for a finding's own sentence.
func scopeWords(scope RoleScope) string {
	if scope == RoleScopeCluster {
		return "in every namespace of the cluster"
	}
	return "in this role's namespace"
}

// wildcardFindings flags `*` in a rule's verbs, resources or API groups.
//
// Three findings rather than one, because each permits something different
// and an operator narrowing a role edits a different field for each: `*`
// verbs is every action on a named resource, `*` resources is every object
// kind in a named group, and `*` apiGroups is every group the cluster serves
// — including groups an operator installs next week and has never seen.
func wildcardFindings(review RoleReview) []RBACFinding {
	var verbs, resources, groups bool
	for _, rule := range review.Rules {
		verbs = verbs || slices.Contains(rule.Verbs, wildcard)
		resources = resources || slices.Contains(rule.Resources, wildcard)
		groups = groups || slices.Contains(rule.APIGroups, wildcard)
	}

	severity := wildcardWorth(review.Scope)
	reach := scopeWords(review.Scope)

	findings := make([]RBACFinding, 0, 3)
	if verbs {
		findings = append(findings, RBACFinding{
			ID:       "rbac:wildcard-verbs",
			Severity: severity,
			Title:    "Every verb",
			Detail: "A rule grants `*` verbs, which is every action the API server offers on the " +
				"resources it names " + reach + " — including delete, and including verbs added by a " +
				"future Kubernetes release.",
			Advice: "List the verbs actually needed. `*` also silently covers escalate, bind and " +
				"impersonate wherever the resources it names carry them.",
		})
	}
	if resources {
		findings = append(findings, RBACFinding{
			ID:       "rbac:wildcard-resources",
			Severity: severity,
			Title:    "Every resource",
			Detail: "A rule grants `*` resources, which is every kind in the API groups it names " +
				reach + " — Secrets included, and every custom resource an operator installs later.",
			Advice: "Name the resources needed. A wildcard here grows on its own as the cluster " +
				"gains CRDs, so what it permits next month is not what was reviewed today.",
		})
	}
	if groups {
		findings = append(findings, RBACFinding{
			ID:       "rbac:wildcard-api-groups",
			Severity: severity,
			Title:    "Every API group",
			Detail: "A rule grants `*` apiGroups, which reaches the core group, every built-in " +
				"group and every group a CRD introduces " + reach + ".",
			Advice: "Name the groups needed — most workloads use one. This is the widest of the " +
				"three wildcards, because a group nobody has installed yet is already covered.",
		})
	}
	return findings
}

// escalationVerb is one of RBAC's three verbs whose whole purpose is to grant
// or assume permissions the holder does not have.
type escalationVerb struct {
	verb   string
	id     string
	title  string
	detail string
	advice string
}

// escalationVerbs are those three, with what each actually permits.
//
// They are flagged on an EXPLICIT mention only. A rule with `*` verbs covers
// all three as well, and is already flagged as a wildcard — listing the same
// rule again under three more headings would bury the roles where somebody
// deliberately granted exactly one of them, which is the case worth seeing.
var escalationVerbs = []escalationVerb{
	{
		verb:  "escalate",
		id:    "rbac:verb-escalate",
		title: "Can escalate",
		detail: "The `escalate` verb waives the check that normally stops an account granting " +
			"permissions it does not itself hold, so the holder can write any rule into the roles " +
			"this rule names.",
		advice: "This is a privilege-escalation primitive by design — it exists so a controller can " +
			"manage roles wider than its own. Grant it only to something that provably needs it.",
	},
	{
		verb:  "bind",
		id:    "rbac:verb-bind",
		title: "Can bind",
		detail: "The `bind` verb lets the holder create bindings to the roles this rule names even " +
			"though it does not hold those roles' permissions itself.",
		advice: "Whoever holds this can hand out every permission the referenced roles carry. Check " +
			"which roles the rule names — `bind` on cluster-admin is cluster-admin.",
	},
	{
		verb:  "impersonate",
		id:    "rbac:verb-impersonate",
		title: "Can impersonate",
		detail: "The `impersonate` verb lets the holder act as another user, group or service " +
			"account, and is then authorised as that account rather than as itself.",
		advice: "Impersonating a cluster administrator is administrator access, and the audit log " +
			"records both identities — so this is traceable, not contained. Narrow it with " +
			"resourceNames, or remove it.",
	},
}

// escalationVerbFindings flags escalate, bind and impersonate.
//
// Always critical, at either scope: unlike a wildcard, none of these three is
// bounded by the namespace it is granted in. A namespaced `impersonate`
// impersonates an account whose own permissions are wherever they are.
func escalationVerbFindings(review RoleReview) []RBACFinding {
	findings := make([]RBACFinding, 0, len(escalationVerbs))
	for _, candidate := range escalationVerbs {
		granted := false
		for _, rule := range review.Rules {
			if slices.Contains(rule.Verbs, candidate.verb) {
				granted = true
				break
			}
		}
		if !granted {
			continue
		}
		findings = append(findings, RBACFinding{
			ID:       candidate.id,
			Severity: SeverityCritical,
			Title:    candidate.title,
			Detail:   candidate.detail,
			Advice:   candidate.advice,
		})
	}
	return findings
}

// readVerbs are the verbs that return a Secret's contents. `deletecollection`
// is not one of them and neither is `patch`: this flag is about what leaves
// the cluster, not about what can be changed.
var readVerbs = []string{"get", "list", "watch"}

// isCoreGroup reports whether groups names the core group, which RBAC writes
// as the empty string.
func isCoreGroup(groups []string) bool {
	return slices.Contains(groups, "") || slices.Contains(groups, wildcard)
}

// grantsRead reports whether verbs permit reading.
func grantsRead(verbs []string) bool {
	if slices.Contains(verbs, wildcard) {
		return true
	}
	for _, verb := range readVerbs {
		if slices.Contains(verbs, verb) {
			return true
		}
	}
	return false
}

// clusterSecretFindings flags reading Secrets at cluster scope.
//
// NAMESPACED SECRET READS ARE NOT FLAGGED, and that is the point of the rule
// rather than an omission: mounting a Secret is how a workload is configured,
// so a Role granting `get secrets` in one namespace describes almost every
// application in Kubernetes. The same rule on a ClusterRole reads every
// Secret in every namespace — every service account token, every registry
// credential, every TLS private key the cluster holds — which is a different
// fact entirely.
//
// The resource is matched EXPLICITLY: a ClusterRole with `*` resources also
// reaches Secrets, and is already flagged as a wildcard.
func clusterSecretFindings(review RoleReview) []RBACFinding {
	if review.Scope != RoleScopeCluster {
		return nil
	}

	for _, rule := range review.Rules {
		if !isCoreGroup(rule.APIGroups) || !slices.Contains(rule.Resources, "secrets") {
			continue
		}
		if !grantsRead(rule.Verbs) {
			continue
		}
		// A rule narrowed to named Secrets reaches those objects in every
		// namespace, which is far narrower than every Secret and not the
		// thing this flag is about.
		if len(rule.ResourceNames) > 0 {
			continue
		}
		return []RBACFinding{{
			ID:       "rbac:cluster-secret-read",
			Severity: SeverityCritical,
			Title:    "Reads every Secret",
			Detail: "A cluster-scoped rule grants read access to Secrets in every namespace — " +
				"service account tokens, registry credentials, TLS private keys and whatever else " +
				"the cluster holds.",
			Advice: "A service account token read this way authenticates as that account, so this " +
				"reaches whatever the most powerful account in the cluster can do. Scope it to a " +
				"namespace with a Role, or to named Secrets with resourceNames.",
		}}
	}
	return nil
}

// podCreationFindings flags creating pods.
//
// Creating a pod means choosing its `serviceAccountName`, and the kubelet
// then mounts that account's token into the container — so whoever may create
// a pod in a namespace holds, indirectly, whatever the most powerful service
// account reachable from that namespace holds. It is the best-known
// escalation path in Kubernetes RBAC and it is invisible in the rule itself,
// which says nothing but "create pods".
//
// ONLY `pods` IS MATCHED, not the controllers that create pods on an
// operator's behalf. A Deployment rule leads to the same place by a longer
// route, but claiming so would be a statement about controller behaviour
// rather than a reading of the rule in front of us, and this file quotes
// where it can and argues only where it must.
func podCreationFindings(review RoleReview) []RBACFinding {
	granted := false
	for _, rule := range review.Rules {
		if !isCoreGroup(rule.APIGroups) || !slices.Contains(rule.Resources, "pods") {
			continue
		}
		if slices.Contains(rule.Verbs, "create") || slices.Contains(rule.Verbs, wildcard) {
			granted = true
			break
		}
	}
	if !granted {
		return nil
	}

	severity := SeverityWarning
	if review.Scope == RoleScopeCluster {
		severity = SeverityCritical
	}

	return []RBACFinding{{
		ID:       "rbac:create-pods",
		Severity: severity,
		Title:    "Creates pods",
		Detail: "A rule grants `create` on pods " + scopeWords(review.Scope) + ". A pod names the " +
			"service account it runs as, and the kubelet mounts that account's token into the " +
			"container.",
		Advice: "This reaches whatever the most powerful service account within reach can do, so it " +
			"is worth reviewing beside the accounts in those namespaces rather than on its own. " +
			"Host mounts and privileged containers are the same rule's other half.",
	}}
}

// clusterAdminBindingFindings flags a binding to cluster-admin.
//
// Read off the binding's own roleRef rather than off the rules, because that
// is where the fact is: cluster-admin is a ClusterRole every conformant
// cluster ships, and a binding naming it grants everything regardless of what
// the rules read at the moment somebody looked.
func clusterAdminBindingFindings(review RoleReview) []RBACFinding {
	subjects := 0
	names := make([]string, 0, 2)
	for _, binding := range review.Bindings {
		if binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != clusterAdminRole {
			continue
		}
		subjects += len(binding.Subjects)
		names = append(names, binding.Name)
	}
	if len(names) == 0 {
		return nil
	}

	slices.Sort(names)
	return []RBACFinding{{
		ID:       "rbac:binds-cluster-admin",
		Severity: SeverityCritical,
		Title:    "Binds cluster-admin",
		Detail: fmt.Sprintf("%s (%s) grants the cluster-admin ClusterRole to %s.",
			countedBindings(len(names)), strings.Join(names, ", "), countedSubjects(subjects)),
		Advice: "cluster-admin is unrestricted: every verb, every resource, every namespace, plus " +
			"the non-resource URLs. Anything that does not need all of it needs a different role.",
	}}
}

// countedBindings renders "1 binding" / "3 bindings".
func countedBindings(count int) string {
	if count == 1 {
		return "1 binding"
	}
	return fmt.Sprintf("%d bindings", count)
}

// countedSubjects renders the subject count, including none — a binding with
// no subjects grants nothing to anybody and saying "0 subjects" is the honest
// reading of it.
func countedSubjects(count int) string {
	if count == 1 {
		return "1 subject"
	}
	return fmt.Sprintf("%d subjects", count)
}

// BindingsReferencing returns the bindings that grant target, applying
// Kubernetes' own reference rules.
//
// THE MATCH IS A RULE, NOT A QUOTATION, which is why it is here with a test
// per case rather than in the adapter that did the listing. Three of them are
// easy to get wrong:
//
//   - A ClusterRoleBinding can only reference a ClusterRole. There is no such
//     thing as a ClusterRoleBinding to a Role, and the API server rejects one.
//   - A RoleBinding can reference a ClusterRole, and that is the ordinary way
//     a shared role such as `view` is granted in one namespace. Missing this
//     case is how a reverse lookup reports a widely used ClusterRole as bound
//     to nobody.
//   - A RoleBinding referencing a Role can only reference one IN ITS OWN
//     NAMESPACE. Matching on the name alone would attribute another
//     namespace's identically named Role — and `Role/edit` exists in a great
//     many namespaces at once.
//
// The result keeps the input order, so a caller that listed cluster-scoped
// bindings before namespaced ones gets them back that way.
func BindingsReferencing(target RoleTarget, bindings []RoleBindingRef) []RoleBindingRef {
	matched := make([]RoleBindingRef, 0, len(bindings))
	for _, binding := range bindings {
		if binding.RoleRef.Name != target.Name {
			continue
		}
		if binding.RoleRef.Kind != target.Kind() {
			continue
		}
		// A namespaced Role is reachable only from its own namespace. A
		// ClusterRole is reachable from anywhere, by either kind of binding.
		if target.Scope == RoleScopeNamespace && binding.Namespace != target.Namespace {
			continue
		}
		matched = append(matched, binding)
	}
	return matched
}
