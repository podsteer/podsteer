package k8s

import (
	"context"
	"fmt"

	authzv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/podsteer/podsteer/app/domain"
)

// This file is the RBAC explorer's transport, and it is deliberately thin.
// Every method here asks the API server a question and carries the answer
// back unaltered: nothing decides whether an account may do something, and
// nothing filters a binding list down to what references a role. The first is
// the API server's job and the second is the domain's (see
// domain.BindingsReferencing), which leaves this file with the mapping
// between two vocabularies and nothing else.
//
// NOTHING HERE IS CACHED, unlike the served-API discovery in upgrade.go
// beside it. An allow or deny decision held for even a few seconds could
// report a permission that has since been revoked as still granted, which is
// the one answer this feature must never give; and the binding lists are
// bounded by being read once when a panel opens rather than by a TTL.

// serviceAccountUserPrefix is how Kubernetes names a service account as an
// authenticated user — `system:serviceaccount:<namespace>:<name>`. A
// SubjectAccessReview has no service-account field, so a subject of that kind
// is asked about under this name, exactly as the authenticator would present
// it.
const serviceAccountUserPrefix = "system:serviceaccount:"

// SubjectRules asks what the current credentials may do in one namespace.
func (a *Adapter) SubjectRules(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
) (domain.RulesReview, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.RulesReview{}, err
	}

	// A rules review has no cluster-wide form: the API rejects a blank
	// namespace, and "what may I do everywhere" is not a question it answers.
	// OrDefault turns the navigator's "all namespaces" into the same
	// namespace kubectl would have used.
	scope := namespace.OrDefault()

	review, err := client.AuthorizationV1().SelfSubjectRulesReviews().Create(
		ctx,
		&authzv1.SelfSubjectRulesReview{
			Spec: authzv1.SelfSubjectRulesReviewSpec{Namespace: scope.String()},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return domain.RulesReview{}, classify(
			fmt.Sprintf("reviewing own rules in %q of %q", scope, id), err)
	}

	return domain.RulesReview{
		Resource:         mapResourceRules(review.Status.ResourceRules),
		NonResource:      mapNonResourceRules(review.Status.NonResourceRules),
		Incomplete:       review.Status.Incomplete,
		IncompleteReason: review.Status.EvaluationError,
	}, nil
}

// AccessReview asks whether one action is permitted, for the current account
// or for a named subject.
func (a *Adapter) AccessReview(
	ctx context.Context,
	id domain.ClusterID,
	request domain.AccessRequest,
) (domain.AccessOutcome, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return domain.AccessOutcome{}, err
	}

	attributes := &authzv1.ResourceAttributes{
		Namespace:   request.Namespace.String(),
		Verb:        request.Verb,
		Group:       request.Group,
		Resource:    request.Resource,
		Subresource: request.Subresource,
		Name:        request.Name,
	}
	op := fmt.Sprintf("reviewing access to %q in %q", request.Resource, id)

	// The current account's own question goes to the self review, which any
	// authenticated account may ask. Asking the same question through a
	// SubjectAccessReview about oneself would work and would need a
	// permission most accounts do not hold — so the two are genuinely
	// different calls rather than one with a field filled in.
	if request.Subject.IsZero() {
		review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(
			ctx,
			&authzv1.SelfSubjectAccessReview{
				Spec: authzv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attributes},
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			return domain.AccessOutcome{}, classify(op, err)
		}
		return mapAccessStatus(review.Status), nil
	}

	spec := authzv1.SubjectAccessReviewSpec{ResourceAttributes: attributes}
	switch request.Subject.Kind {
	case domain.SubjectGroup:
		spec.Groups = []string{request.Subject.Name}
	case domain.SubjectServiceAccount:
		spec.User = serviceAccountUserPrefix +
			request.Subject.Namespace.OrDefault().String() + ":" + request.Subject.Name
	default:
		spec.User = request.Subject.Name
	}

	review, err := client.AuthorizationV1().SubjectAccessReviews().Create(
		ctx, &authzv1.SubjectAccessReview{Spec: spec}, metav1.CreateOptions{})
	if err != nil {
		return domain.AccessOutcome{}, classify(op, err)
	}
	return mapAccessStatus(review.Status), nil
}

// RoleRules returns one Role or ClusterRole's own rules.
func (a *Adapter) RoleRules(
	ctx context.Context,
	id domain.ClusterID,
	target domain.RoleTarget,
) ([]domain.PolicyRule, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	op := fmt.Sprintf("reading %s %q of %q", target.Kind(), target.Name, id)

	if target.Scope == domain.RoleScopeCluster {
		role, err := client.RbacV1().ClusterRoles().Get(ctx, target.Name, metav1.GetOptions{})
		if err != nil {
			return nil, classify(op, err)
		}
		return mapPolicyRules(role.Rules), nil
	}

	role, err := client.RbacV1().Roles(target.Namespace.String()).Get(ctx, target.Name, metav1.GetOptions{})
	if err != nil {
		return nil, classify(op, err)
	}
	return mapPolicyRules(role.Rules), nil
}

// ListBindings returns every ClusterRoleBinding and every RoleBinding.
//
// Cluster-scoped bindings come first, so the reverse lookup's result — which
// keeps input order — reads outermost first, the way the panel shows it.
//
// A SINGLE FAILURE FAILS THE CALL, unlike the namespace inventory's
// per-kind refusals. The two lists answer one question between them, and a
// half answer would say a role is bound to fewer subjects than it is, which
// is the direction it must not be wrong in.
func (a *Adapter) ListBindings(ctx context.Context, id domain.ClusterID) ([]domain.RoleBindingRef, error) {
	client, err := a.factory.clientFor(id)
	if err != nil {
		return nil, err
	}

	// The same watch-cache read every other poll here uses: a binding graph
	// seconds out of date is not worth a quorum read, and this is the one
	// call the panel makes.
	options := metav1.ListOptions{ResourceVersion: cachedResourceVersion}

	clusterBindings, err := client.RbacV1().ClusterRoleBindings().List(ctx, options)
	if err != nil {
		return nil, classify(fmt.Sprintf("listing cluster role bindings in %q", id), err)
	}

	// Every namespace at once — a ClusterRole is referenced by RoleBindings
	// anywhere, so narrowing this to the tab's namespace would report a
	// widely granted role as bound to nobody.
	roleBindings, err := client.RbacV1().RoleBindings(metav1.NamespaceAll).List(ctx, options)
	if err != nil {
		return nil, classify(fmt.Sprintf("listing role bindings in %q", id), err)
	}

	bindings := make([]domain.RoleBindingRef, 0, len(clusterBindings.Items)+len(roleBindings.Items))
	for _, binding := range clusterBindings.Items {
		bindings = append(bindings, domain.RoleBindingRef{
			Kind:     "ClusterRoleBinding",
			Name:     binding.Name,
			RoleRef:  domain.RoleRef{Kind: binding.RoleRef.Kind, Name: binding.RoleRef.Name},
			Subjects: mapSubjects(binding.Subjects),
		})
	}
	for _, binding := range roleBindings.Items {
		bindings = append(bindings, domain.RoleBindingRef{
			Kind:      "RoleBinding",
			Name:      binding.Name,
			Namespace: domain.NamespaceName(binding.Namespace),
			RoleRef:   domain.RoleRef{Kind: binding.RoleRef.Kind, Name: binding.RoleRef.Name},
			Subjects:  mapSubjects(binding.Subjects),
		})
	}
	return bindings, nil
}

// mapAccessStatus carries the API server's verdict across unchanged.
//
// Allowed and Denied are both copied because they are not opposites in this
// API: an authorizer with no opinion leaves both false, and reporting that as
// a denial would claim a verdict nothing gave.
func mapAccessStatus(status authzv1.SubjectAccessReviewStatus) domain.AccessOutcome {
	return domain.AccessOutcome{
		Allowed:         status.Allowed,
		Denied:          status.Denied,
		Reason:          status.Reason,
		EvaluationError: status.EvaluationError,
	}
}

// mapPolicyRules converts rbac/v1 rules into their domain form.
func mapPolicyRules(rules []rbacv1.PolicyRule) []domain.PolicyRule {
	mapped := make([]domain.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		mapped = append(mapped, domain.PolicyRule{
			Verbs:           rule.Verbs,
			APIGroups:       rule.APIGroups,
			Resources:       rule.Resources,
			ResourceNames:   rule.ResourceNames,
			NonResourceURLs: rule.NonResourceURLs,
		})
	}
	return mapped
}

// mapResourceRules converts a rules review's resource half.
func mapResourceRules(rules []authzv1.ResourceRule) []domain.PolicyRule {
	mapped := make([]domain.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		mapped = append(mapped, domain.PolicyRule{
			Verbs:         rule.Verbs,
			APIGroups:     rule.APIGroups,
			Resources:     rule.Resources,
			ResourceNames: rule.ResourceNames,
		})
	}
	return mapped
}

// mapNonResourceRules converts a rules review's non-resource half — the URL
// paths, which belong to the API server rather than to any namespace.
func mapNonResourceRules(rules []authzv1.NonResourceRule) []domain.PolicyRule {
	mapped := make([]domain.PolicyRule, 0, len(rules))
	for _, rule := range rules {
		mapped = append(mapped, domain.PolicyRule{
			Verbs:           rule.Verbs,
			NonResourceURLs: rule.NonResourceURLs,
		})
	}
	return mapped
}

// mapSubjects converts a binding's subjects.
//
// The namespace is carried only for a ServiceAccount, which is the only
// subject kind that is an object in a namespace. A User or a Group with a
// namespace attached would suggest the account is scoped to it, and no
// authenticator works that way.
func mapSubjects(subjects []rbacv1.Subject) []domain.RBACSubject {
	mapped := make([]domain.RBACSubject, 0, len(subjects))
	for _, subject := range subjects {
		kind := domain.SubjectKind(subject.Kind)
		namespace := domain.NamespaceAll
		if kind == domain.SubjectServiceAccount {
			namespace = domain.NamespaceName(subject.Namespace)
		}
		mapped = append(mapped, domain.RBACSubject{
			Kind:      kind,
			Name:      subject.Name,
			Namespace: namespace,
		})
	}
	return mapped
}
