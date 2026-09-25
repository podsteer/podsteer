package domain_test

import (
	"slices"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// findingIDs lists the ids raised, for comparing a whole assessment at once.
func findingIDs(findings []domain.RBACFinding) []string {
	ids := make([]string, 0, len(findings))
	for _, finding := range findings {
		ids = append(ids, finding.ID)
	}
	return ids
}

// hasFinding reports whether id was raised.
func hasFinding(findings []domain.RBACFinding, id string) bool {
	return slices.Contains(findingIDs(findings), id)
}

// findFinding returns the finding with id, or the zero value.
func findFinding(findings []domain.RBACFinding, id string) domain.RBACFinding {
	for _, finding := range findings {
		if finding.ID == id {
			return finding
		}
	}
	return domain.RBACFinding{}
}

// readOnlyDeploymentRule is the shape of a rule that should never flag: a
// narrow, explicit, namespaced read.
var readOnlyDeploymentRule = domain.PolicyRule{
	Verbs:     []string{"get", "list", "watch"},
	APIGroups: []string{"apps"},
	Resources: []string{"deployments"},
}

// TestAssessRoleFlagsEachBlastRadiusRule pins every flag and, beside it, the
// case that must NOT raise it. The negatives are the half worth having: a
// flag that fires on an ordinary role is one an operator learns to ignore.
func TestAssessRoleFlagsEachBlastRadiusRule(t *testing.T) {
	tests := []struct {
		name   string
		review domain.RoleReview
		want   string // the finding id expected, or "" for none
	}{
		{
			name: "wildcard verbs",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"*"},
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
				}},
			},
			want: "rbac:wildcard-verbs",
		},
		{
			name: "explicit verbs do not read as a wildcard",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
					APIGroups: []string{"apps"},
					Resources: []string{"deployments"},
				}},
			},
			want: "",
		},
		{
			name: "wildcard resources",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get"},
					APIGroups: []string{"apps"},
					Resources: []string{"*"},
				}},
			},
			want: "rbac:wildcard-resources",
		},
		{
			name: "wildcard api groups",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get"},
					APIGroups: []string{"*"},
					Resources: []string{"deployments"},
				}},
			},
			want: "rbac:wildcard-api-groups",
		},
		{
			name: "the core group is not a wildcard",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				}},
			},
			want: "",
		},
		{
			name: "escalate",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"escalate"},
					APIGroups: []string{"rbac.authorization.k8s.io"},
					Resources: []string{"clusterroles"},
				}},
			},
			want: "rbac:verb-escalate",
		},
		{
			name: "bind",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"bind"},
					APIGroups: []string{"rbac.authorization.k8s.io"},
					Resources: []string{"clusterroles"},
				}},
			},
			want: "rbac:verb-bind",
		},
		{
			name: "impersonate",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"impersonate"},
					APIGroups: []string{""},
					Resources: []string{"serviceaccounts"},
				}},
			},
			want: "rbac:verb-impersonate",
		},
		{
			name: "reading roles is not escalating them",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{"rbac.authorization.k8s.io"},
					Resources: []string{"clusterroles"},
				}},
			},
			want: "",
		},
		{
			name: "cluster-scoped secret read",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				}},
			},
			want: "rbac:cluster-secret-read",
		},
		{
			name: "the same secret read in one namespace is ordinary",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				}},
			},
			want: "",
		},
		{
			name: "writing secrets without reading them is not this flag",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"create", "update"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				}},
			},
			want: "",
		},
		{
			name: "a named secret is not every secret",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Rules: []domain.PolicyRule{{
					Verbs:         []string{"get"},
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: []string{"registry-pull"},
				}},
			},
			want: "",
		},
		{
			name: "creating pods",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"create"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				}},
			},
			want: "rbac:create-pods",
		},
		{
			name: "reading pods is not creating them",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"get", "list", "watch", "delete"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				}},
			},
			want: "",
		},
		{
			name: "creating pods in another API group is not creating pods",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Rules: []domain.PolicyRule{{
					Verbs:     []string{"create"},
					APIGroups: []string{"example.com"},
					Resources: []string{"pods"},
				}},
			},
			want: "",
		},
		{
			name: "a binding to cluster-admin",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Name:  "cluster-admin",
				Bindings: []domain.RoleBindingRef{{
					Kind:     "ClusterRoleBinding",
					Name:     "platform-admins",
					RoleRef:  domain.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"},
					Subjects: []domain.RBACSubject{{Kind: domain.SubjectGroup, Name: "sre"}},
				}},
			},
			want: "rbac:binds-cluster-admin",
		},
		{
			name: "a binding to any other role",
			review: domain.RoleReview{
				Scope: domain.RoleScopeCluster,
				Name:  "view",
				Bindings: []domain.RoleBindingRef{{
					Kind:     "ClusterRoleBinding",
					Name:     "everyone-reads",
					RoleRef:  domain.RoleRef{Kind: "ClusterRole", Name: "view"},
					Subjects: []domain.RBACSubject{{Kind: domain.SubjectGroup, Name: "sre"}},
				}},
			},
			want: "",
		},
		{
			name: "a Role that happens to be called cluster-admin is not the ClusterRole",
			review: domain.RoleReview{
				Scope: domain.RoleScopeNamespace,
				Name:  "cluster-admin",
				Bindings: []domain.RoleBindingRef{{
					Kind:     "RoleBinding",
					Name:     "local",
					RoleRef:  domain.RoleRef{Kind: "Role", Name: "cluster-admin"},
					Subjects: []domain.RBACSubject{{Kind: domain.SubjectUser, Name: "ada"}},
				}},
			},
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := domain.AssessRole(test.review)

			if test.want == "" {
				if len(findings) != 0 {
					t.Fatalf("expected no findings, got %v", findingIDs(findings))
				}
				return
			}
			if !hasFinding(findings, test.want) {
				t.Fatalf("expected finding %q, got %v", test.want, findingIDs(findings))
			}
		})
	}
}

// TestAssessRoleSaysNothingAboutAnOrdinaryRole is the rule that keeps the
// panel readable, and it is asserted the same way pod_assessment_test.go
// asserts it for a correctly configured pod.
func TestAssessRoleSaysNothingAboutAnOrdinaryRole(t *testing.T) {
	review := domain.RoleReview{
		Scope: domain.RoleScopeNamespace,
		Name:  "deployment-reader",
		Rules: []domain.PolicyRule{
			readOnlyDeploymentRule,
			{
				Verbs:     []string{"get", "list"},
				APIGroups: []string{""},
				Resources: []string{"pods", "configmaps"},
			},
		},
		Bindings: []domain.RoleBindingRef{{
			Kind:     "RoleBinding",
			Name:     "readers",
			RoleRef:  domain.RoleRef{Kind: "Role", Name: "deployment-reader"},
			Subjects: []domain.RBACSubject{{Kind: domain.SubjectUser, Name: "ada"}},
		}},
	}

	if findings := domain.AssessRole(review); len(findings) != 0 {
		t.Fatalf("an ordinary read-only role must produce no findings, got %v", findingIDs(findings))
	}
}

// TestAssessRoleRanksWildcardsByScope pins the one severity rule that is not
// obvious: the identical wildcard rule is a warning in a namespace and
// critical cluster-wide, because the token means a different amount of
// cluster in each case.
func TestAssessRoleRanksWildcardsByScope(t *testing.T) {
	rules := []domain.PolicyRule{{
		Verbs:     []string{"*"},
		APIGroups: []string{"apps"},
		Resources: []string{"deployments"},
	}}

	namespaced := findFinding(
		domain.AssessRole(domain.RoleReview{Scope: domain.RoleScopeNamespace, Rules: rules}),
		"rbac:wildcard-verbs")
	if namespaced.Severity != domain.SeverityWarning {
		t.Errorf("namespaced wildcard: want %q, got %q", domain.SeverityWarning, namespaced.Severity)
	}

	clusterWide := findFinding(
		domain.AssessRole(domain.RoleReview{Scope: domain.RoleScopeCluster, Rules: rules}),
		"rbac:wildcard-verbs")
	if clusterWide.Severity != domain.SeverityCritical {
		t.Errorf("cluster-scoped wildcard: want %q, got %q", domain.SeverityCritical, clusterWide.Severity)
	}
}

// TestAssessRoleRanksFindingsWorstFirst pins that the order on screen is the
// severity order rather than the order the rules happen to run in.
func TestAssessRoleRanksFindingsWorstFirst(t *testing.T) {
	// Wildcard resources at namespace scope is a warning; impersonate is
	// always critical — and the wildcard rule runs first inside AssessRole.
	findings := domain.AssessRole(domain.RoleReview{
		Scope: domain.RoleScopeNamespace,
		Rules: []domain.PolicyRule{
			{Verbs: []string{"get"}, APIGroups: []string{"apps"}, Resources: []string{"*"}},
			{Verbs: []string{"impersonate"}, APIGroups: []string{""}, Resources: []string{"users"}},
		},
	})

	if len(findings) < 2 {
		t.Fatalf("expected both findings, got %v", findingIDs(findings))
	}
	if findings[0].Severity != domain.SeverityCritical {
		t.Errorf("worst finding must lead, got %q first", findings[0].ID)
	}
}

// TestAssessRoleGivesEveryFindingAdvice pins the rule the whole feature rests
// on: a flag that does not say why it matters is trivia, and this panel is
// supposed to be the difference between showing the object and saying what it
// means.
func TestAssessRoleGivesEveryFindingAdvice(t *testing.T) {
	findings := domain.AssessRole(domain.RoleReview{
		Scope: domain.RoleScopeCluster,
		Name:  "cluster-admin",
		Rules: []domain.PolicyRule{{
			Verbs:     []string{"*"},
			APIGroups: []string{"*"},
			Resources: []string{"*"},
		}, {
			Verbs:     []string{"escalate", "bind", "impersonate"},
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"clusterroles"},
		}, {
			Verbs:     []string{"get"},
			APIGroups: []string{""},
			Resources: []string{"secrets"},
		}, {
			Verbs:     []string{"create"},
			APIGroups: []string{""},
			Resources: []string{"pods"},
		}},
		Bindings: []domain.RoleBindingRef{{
			Kind:     "ClusterRoleBinding",
			Name:     "cluster-admin",
			RoleRef:  domain.RoleRef{Kind: "ClusterRole", Name: "cluster-admin"},
			Subjects: []domain.RBACSubject{{Kind: domain.SubjectGroup, Name: "system:masters"}},
		}},
	})

	// Every rule above should have fired, which is also the assertion that
	// none of them shadows another.
	want := []string{
		"rbac:wildcard-verbs",
		"rbac:wildcard-resources",
		"rbac:wildcard-api-groups",
		"rbac:verb-escalate",
		"rbac:verb-bind",
		"rbac:verb-impersonate",
		"rbac:cluster-secret-read",
		"rbac:create-pods",
		"rbac:binds-cluster-admin",
	}
	for _, id := range want {
		if !hasFinding(findings, id) {
			t.Errorf("expected finding %q, got %v", id, findingIDs(findings))
		}
	}

	for _, finding := range findings {
		if finding.Title == "" || finding.Detail == "" || finding.Advice == "" {
			t.Errorf("finding %q must carry a title, a detail and advice", finding.ID)
		}
	}
}

// TestBindingsReferencingMatchesKubernetesOwnRules covers each shape of
// reference a cluster can hold, and the two near misses that a name-only
// match would wrongly claim.
func TestBindingsReferencingMatchesKubernetesOwnRules(t *testing.T) {
	clusterRoleBinding := domain.RoleBindingRef{
		Kind:    "ClusterRoleBinding",
		Name:    "view-everywhere",
		RoleRef: domain.RoleRef{Kind: "ClusterRole", Name: "view"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectGroup, Name: "developers"},
		},
	}
	roleBindingToClusterRole := domain.RoleBindingRef{
		Kind:      "RoleBinding",
		Name:      "view-in-shop",
		Namespace: domain.NamespaceName("shop"),
		RoleRef:   domain.RoleRef{Kind: "ClusterRole", Name: "view"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectServiceAccount, Name: "reader", Namespace: domain.NamespaceName("shop")},
		},
	}
	roleBindingToRole := domain.RoleBindingRef{
		Kind:      "RoleBinding",
		Name:      "editors",
		Namespace: domain.NamespaceName("shop"),
		RoleRef:   domain.RoleRef{Kind: "Role", Name: "editor"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectUser, Name: "ada"},
		},
	}
	// The same Role name in another namespace. `editor` exists in a great
	// many namespaces at once, so this is the case a name-only match breaks.
	roleBindingElsewhere := domain.RoleBindingRef{
		Kind:      "RoleBinding",
		Name:      "editors",
		Namespace: domain.NamespaceName("billing"),
		RoleRef:   domain.RoleRef{Kind: "Role", Name: "editor"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectUser, Name: "grace"},
		},
	}
	// A ClusterRole and a Role sharing a name — the kind has to decide.
	roleBindingSameNameDifferentKind := domain.RoleBindingRef{
		Kind:      "RoleBinding",
		Name:      "local-view",
		Namespace: domain.NamespaceName("shop"),
		RoleRef:   domain.RoleRef{Kind: "Role", Name: "view"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectUser, Name: "linus"},
		},
	}

	all := []domain.RoleBindingRef{
		clusterRoleBinding,
		roleBindingToClusterRole,
		roleBindingToRole,
		roleBindingElsewhere,
		roleBindingSameNameDifferentKind,
	}

	tests := []struct {
		name   string
		target domain.RoleTarget
		want   []string // binding names, in order
	}{
		{
			name:   "a ClusterRole is reached by both kinds of binding",
			target: domain.RoleTarget{Scope: domain.RoleScopeCluster, Name: "view"},
			want:   []string{"view-everywhere", "view-in-shop"},
		},
		{
			name: "a Role is reached only from its own namespace",
			target: domain.RoleTarget{
				Scope:     domain.RoleScopeNamespace,
				Namespace: domain.NamespaceName("shop"),
				Name:      "editor",
			},
			want: []string{"editors"},
		},
		{
			name: "a Role sharing a ClusterRole's name matches only the Role reference",
			target: domain.RoleTarget{
				Scope:     domain.RoleScopeNamespace,
				Namespace: domain.NamespaceName("shop"),
				Name:      "view",
			},
			want: []string{"local-view"},
		},
		{
			name:   "a ClusterRole nothing references",
			target: domain.RoleTarget{Scope: domain.RoleScopeCluster, Name: "edit"},
			want:   []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matched := domain.BindingsReferencing(test.target, all)

			names := make([]string, 0, len(matched))
			for _, binding := range matched {
				names = append(names, binding.Name)
			}
			if !slices.Equal(names, test.want) {
				t.Fatalf("want %v, got %v", test.want, names)
			}
		})
	}
}

// TestBindingsReferencingCarriesEverySubjectKind pins that a matched binding
// arrives with its subjects intact, whichever kind they are — a ServiceAccount
// keeps the namespace that identifies it, and a User and a Group do not
// acquire one.
func TestBindingsReferencingCarriesEverySubjectKind(t *testing.T) {
	binding := domain.RoleBindingRef{
		Kind:    "ClusterRoleBinding",
		Name:    "mixed",
		RoleRef: domain.RoleRef{Kind: "ClusterRole", Name: "view"},
		Subjects: []domain.RBACSubject{
			{Kind: domain.SubjectUser, Name: "ada"},
			{Kind: domain.SubjectGroup, Name: "developers"},
			{Kind: domain.SubjectServiceAccount, Name: "reader", Namespace: domain.NamespaceName("shop")},
		},
	}

	matched := domain.BindingsReferencing(
		domain.RoleTarget{Scope: domain.RoleScopeCluster, Name: "view"},
		[]domain.RoleBindingRef{binding})

	if len(matched) != 1 {
		t.Fatalf("want 1 binding, got %d", len(matched))
	}
	if len(matched[0].Subjects) != 3 {
		t.Fatalf("want 3 subjects, got %d", len(matched[0].Subjects))
	}
	for _, subject := range matched[0].Subjects {
		switch subject.Kind {
		case domain.SubjectServiceAccount:
			if subject.Namespace != domain.NamespaceName("shop") {
				t.Errorf("a ServiceAccount subject must keep its namespace, got %q", subject.Namespace)
			}
		case domain.SubjectUser, domain.SubjectGroup:
			if subject.Namespace != domain.NamespaceAll {
				t.Errorf("a %s subject has no namespace, got %q", subject.Kind, subject.Namespace)
			}
		default:
			t.Errorf("unexpected subject kind %q", subject.Kind)
		}
	}
}

// TestRoleTargetKindNamesTheKubernetesKind pins the mapping the reverse
// lookup matches on, since getting it backwards would silently match nothing.
func TestRoleTargetKindNamesTheKubernetesKind(t *testing.T) {
	if kind := (domain.RoleTarget{Scope: domain.RoleScopeCluster}).Kind(); kind != "ClusterRole" {
		t.Errorf("cluster scope: want ClusterRole, got %q", kind)
	}
	if kind := (domain.RoleTarget{Scope: domain.RoleScopeNamespace}).Kind(); kind != "Role" {
		t.Errorf("namespace scope: want Role, got %q", kind)
	}
}

// TestRBACSubjectIsZeroSelectsTheCurrentAccount pins the flag that decides
// which review API a request reaches: an unnamed subject is the caller's own
// SelfSubjectAccessReview, and anything named is a SubjectAccessReview.
func TestRBACSubjectIsZeroSelectsTheCurrentAccount(t *testing.T) {
	if !(domain.RBACSubject{}).IsZero() {
		t.Error("an unnamed subject must read as the current account")
	}
	if (domain.RBACSubject{Kind: domain.SubjectUser, Name: "ada"}).IsZero() {
		t.Error("a named subject must not read as the current account")
	}
}
