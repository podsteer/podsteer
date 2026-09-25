package wails

import (
	"github.com/podsteer/podsteer/app/domain"
)

// Wire types for the RBAC explorer. As with dto.go these are the frontend API
// contract — Wails generates TypeScript from them — so a field rename is a
// breaking change.
//
// Every one of them carries a STATUS beside its payload rather than relying
// on an error, because being refused is an ordinary answer here: an account
// that cannot ask a review, or cannot list bindings, still gets a panel that
// says which of those happened. See domain.ReviewStatus.

// PolicyRule is one RBAC rule, as the cluster stated it.
type PolicyRule struct {
	// Verbs are the actions permitted. "*" is all of them.
	Verbs []string `json:"verbs"`
	// APIGroups are the groups the resources live in. "" is the core group.
	APIGroups []string `json:"apiGroups"`
	// Resources are the resource plurals, optionally "resource/subresource".
	Resources []string `json:"resources"`
	// ResourceNames narrows the rule to named objects, and is usually empty.
	ResourceNames []string `json:"resourceNames"`
	// NonResourceURLs are URL paths, which belong to the API server rather
	// than to any namespace.
	NonResourceURLs []string `json:"nonResourceUrls"`
}

// SubjectRules is the answer to "what can this kubeconfig do here".
type SubjectRules struct {
	// Namespace is the namespace the review actually asked about, which is
	// not always the one the tab is filtered to — a rules review has no
	// cluster-wide form.
	Namespace string `json:"namespace"`
	// Status is answered, forbidden, absent or failed.
	Status string `json:"status"`
	// Refusal is the sentence to show when Status is not "answered".
	Refusal string `json:"refusal"`
	// Namespaced holds the rules that apply to objects in Namespace.
	Namespaced []PolicyRule `json:"namespaced"`
	// ClusterScoped holds the non-resource URL rules, which are the API
	// server's own paths and so belong to the cluster rather than to the
	// namespace asked about.
	ClusterScoped []PolicyRule `json:"clusterScoped"`
	// Incomplete is the API server's own warning that it could not enumerate
	// everything.
	Incomplete bool `json:"incomplete"`
	// IncompleteReason is the evaluation error behind Incomplete, verbatim.
	IncompleteReason string `json:"incompleteReason"`
}

// RBACSubject is one account a binding names, or one being asked about.
//
// A subject NAME IS AN OBJECT NAME: it crosses this boundary and is shown,
// and it is never written to disk — the same commitment SECURITY.md makes
// about every other object name.
type RBACSubject struct {
	// Kind is "User", "Group" or "ServiceAccount". Empty means the current
	// account, which is what sends a review to the self API instead.
	Kind string `json:"kind"`
	// Name is the account name.
	Name string `json:"name"`
	// Namespace is the ServiceAccount's namespace, and empty for the others.
	Namespace string `json:"namespace"`
}

// AccessRequest is one "can I" question.
//
// The subject is FLATTENED into three strings rather than nested, unlike
// everywhere else here, because this type is an ARGUMENT: Wails generates a
// class with a value-converting method for any struct holding another, and a
// plain object typed against it no longer satisfies the declaration the
// frontend has to call through. Three fields cost nothing and keep the call
// site an object literal.
type AccessRequest struct {
	// SubjectKind is "User", "Group" or "ServiceAccount". Empty, with an
	// empty SubjectName, asks about the current account — which is a
	// different review API rather than a missing field.
	SubjectKind string `json:"subjectKind"`
	// SubjectName is the account being asked about.
	SubjectName string `json:"subjectName"`
	// SubjectNamespace is a ServiceAccount subject's namespace.
	SubjectNamespace string `json:"subjectNamespace"`
	// Verb is the action, e.g. "create". Required.
	Verb string `json:"verb"`
	// Group is the API group, empty for the core group.
	Group string `json:"group"`
	// Resource is the resource plural, e.g. "pods". Required.
	Resource string `json:"resource"`
	// Subresource narrows it, e.g. "log" or "exec".
	Subresource string `json:"subresource"`
	// Namespace scopes the question; empty asks at cluster scope.
	Namespace string `json:"namespace"`
	// Name narrows the question to one object.
	Name string `json:"name"`
}

// AccessDecision is the API server's answer to one question.
type AccessDecision struct {
	// Request is the question, echoed so an answer cannot be read against a
	// form that has since been edited.
	Request AccessRequest `json:"request"`
	// Status is answered, forbidden, absent or failed.
	Status string `json:"status"`
	// Refusal is the sentence to show when Status is not "answered".
	Refusal string `json:"refusal"`
	// Allowed is the API server's own `allowed`.
	Allowed bool `json:"allowed"`
	// Denied is the API server's own `denied`. NOT the opposite of Allowed:
	// an authorizer with no opinion leaves both false, which is a third
	// answer and must render as one.
	Denied bool `json:"denied"`
	// Reason is the API server's own `reason`, verbatim.
	Reason string `json:"reason"`
	// EvaluationError is the API server's own `evaluationError`, verbatim.
	EvaluationError string `json:"evaluationError"`
}

// RoleBindingRef is one RoleBinding or ClusterRoleBinding.
type RoleBindingRef struct {
	// Kind is "RoleBinding" or "ClusterRoleBinding".
	Kind string `json:"kind"`
	// Name is the binding's name.
	Name string `json:"name"`
	// Namespace is the RoleBinding's namespace, empty for a
	// ClusterRoleBinding.
	Namespace string `json:"namespace"`
	// RoleRefKind is the referenced role's kind.
	RoleRefKind string `json:"roleRefKind"`
	// RoleRefName is the referenced role's name.
	RoleRefName string `json:"roleRefName"`
	// Subjects are the accounts it grants the role to.
	Subjects []RBACSubject `json:"subjects"`
}

// RBACFinding is one blast-radius flag.
type RBACFinding struct {
	// ID is stable for the same flag across inspections.
	ID string `json:"id"`
	// Severity is critical, warning or info.
	Severity string `json:"severity"`
	// Title is the power granted, in a few words.
	Title string `json:"title"`
	// Detail says what it permits, in one sentence.
	Detail string `json:"detail"`
	// Advice says why it matters.
	Advice string `json:"advice"`
}

// RoleInspection is one Role or ClusterRole, what references it, and what its
// rules permit.
type RoleInspection struct {
	// Scope is "cluster" or "namespace".
	Scope string `json:"scope"`
	// Namespace is the Role's namespace, empty for a ClusterRole.
	Namespace string `json:"namespace"`
	// Name is the role's name.
	Name string `json:"name"`
	// Kind is the Kubernetes kind Scope names.
	Kind string `json:"kind"`
	// Status says whether the role itself could be read.
	Status string `json:"status"`
	// Refusal is the sentence to show when Status is not "answered".
	Refusal string `json:"refusal"`
	// Rules are the role's own rules.
	Rules []PolicyRule `json:"rules"`
	// BindingsStatus says whether the bindings could be listed. Separate
	// from Status because the two are separate reads and fail apart.
	BindingsStatus string `json:"bindingsStatus"`
	// BindingsRefusal is the sentence to show when BindingsStatus is not
	// "answered".
	BindingsRefusal string `json:"bindingsRefusal"`
	// Bindings are the bindings that reference this role.
	Bindings []RoleBindingRef `json:"bindings"`
	// Findings are the blast-radius flags, worst first.
	Findings []RBACFinding `json:"findings"`
}

// toPolicyRules converts domain rules. The result is always non-nil so it
// marshals to [] rather than null, the same rule toClusters follows.
func toPolicyRules(rules []domain.PolicyRule) []PolicyRule {
	out := make([]PolicyRule, 0, len(rules))
	for _, rule := range rules {
		out = append(out, PolicyRule{
			Verbs:           orEmpty(rule.Verbs),
			APIGroups:       orEmpty(rule.APIGroups),
			Resources:       orEmpty(rule.Resources),
			ResourceNames:   orEmpty(rule.ResourceNames),
			NonResourceURLs: orEmpty(rule.NonResourceURLs),
		})
	}
	return out
}

// orEmpty returns values, or an empty slice so it marshals to [].
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// toSubjectRules converts a rules review for the wire.
func toSubjectRules(rules domain.SubjectRules) SubjectRules {
	return SubjectRules{
		Namespace:        rules.Namespace.String(),
		Status:           string(rules.Status),
		Refusal:          rules.Refusal,
		Namespaced:       toPolicyRules(rules.Review.Resource),
		ClusterScoped:    toPolicyRules(rules.Review.NonResource),
		Incomplete:       rules.Review.Incomplete,
		IncompleteReason: rules.Review.IncompleteReason,
	}
}

// toRBACSubject converts one subject for the wire.
func toRBACSubject(subject domain.RBACSubject) RBACSubject {
	return RBACSubject{
		Kind:      string(subject.Kind),
		Name:      subject.Name,
		Namespace: subject.Namespace.String(),
	}
}

// toRBACSubjects converts a binding's subjects.
func toRBACSubjects(subjects []domain.RBACSubject) []RBACSubject {
	out := make([]RBACSubject, 0, len(subjects))
	for _, subject := range subjects {
		out = append(out, toRBACSubject(subject))
	}
	return out
}

// toAccessRequest converts a request back for the wire, so a decision carries
// the question it answered.
func toAccessRequest(request domain.AccessRequest) AccessRequest {
	return AccessRequest{
		SubjectKind:      string(request.Subject.Kind),
		SubjectName:      request.Subject.Name,
		SubjectNamespace: request.Subject.Namespace.String(),

		Verb:        request.Verb,
		Group:       request.Group,
		Resource:    request.Resource,
		Subresource: request.Subresource,
		Namespace:   request.Namespace.String(),
		Name:        request.Name,
	}
}

// toAccessDecision converts one decision for the wire.
func toAccessDecision(decision domain.AccessDecision) AccessDecision {
	return AccessDecision{
		Request:         toAccessRequest(decision.Request),
		Status:          string(decision.Status),
		Refusal:         decision.Refusal,
		Allowed:         decision.Outcome.Allowed,
		Denied:          decision.Outcome.Denied,
		Reason:          decision.Outcome.Reason,
		EvaluationError: decision.Outcome.EvaluationError,
	}
}

// toRoleBindings converts the reverse lookup's result.
func toRoleBindings(bindings []domain.RoleBindingRef) []RoleBindingRef {
	out := make([]RoleBindingRef, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, RoleBindingRef{
			Kind:        binding.Kind,
			Name:        binding.Name,
			Namespace:   binding.Namespace.String(),
			RoleRefKind: binding.RoleRef.Kind,
			RoleRefName: binding.RoleRef.Name,
			Subjects:    toRBACSubjects(binding.Subjects),
		})
	}
	return out
}

// toRBACFindings converts the blast-radius flags.
func toRBACFindings(findings []domain.RBACFinding) []RBACFinding {
	out := make([]RBACFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, RBACFinding{
			ID:       finding.ID,
			Severity: string(finding.Severity),
			Title:    finding.Title,
			Detail:   finding.Detail,
			Advice:   finding.Advice,
		})
	}
	return out
}

// toRoleInspection converts a whole inspection for the wire.
func toRoleInspection(inspection domain.RoleInspection) RoleInspection {
	return RoleInspection{
		Scope:           string(inspection.Target.Scope),
		Namespace:       inspection.Target.Namespace.String(),
		Name:            inspection.Target.Name,
		Kind:            inspection.Target.Kind(),
		Status:          string(inspection.Status),
		Refusal:         inspection.Refusal,
		Rules:           toPolicyRules(inspection.Rules),
		BindingsStatus:  string(inspection.BindingsStatus),
		BindingsRefusal: inspection.BindingsRefusal,
		Bindings:        toRoleBindings(inspection.Bindings),
		Findings:        toRBACFindings(inspection.Findings),
	}
}
