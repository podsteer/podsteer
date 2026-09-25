package k8s

import (
	"context"
	"errors"
	"testing"

	authzv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The review APIs are POSTs whose whole answer is in the response's status,
// and the fake clientset's object tracker simply stores what it is handed —
// so every test here installs a reactor that answers the way an API server
// would. That is not a workaround: the answer is the thing under test, and a
// reactor is the only place a fake can produce one.

// answerAccessReview makes the fake answer both access review kinds with the
// given status, and records the object each call sent.
func answerAccessReview(
	client *fake.Clientset,
	status authzv1.SubjectAccessReviewStatus,
	sent *runtime.Object,
) {
	react := func(action clientgotesting.Action) (bool, runtime.Object, error) {
		created := action.(clientgotesting.CreateAction).GetObject()
		if sent != nil {
			*sent = created
		}
		switch object := created.(type) {
		case *authzv1.SelfSubjectAccessReview:
			answered := object.DeepCopy()
			answered.Status = status
			return true, answered, nil
		case *authzv1.SubjectAccessReview:
			answered := object.DeepCopy()
			answered.Status = status
			return true, answered, nil
		}
		return false, nil, nil
	}
	client.PrependReactor("create", "selfsubjectaccessreviews", react)
	client.PrependReactor("create", "subjectaccessreviews", react)
}

// refuse makes the named resource's create return the 403 an API server sends
// an account that may not ask the question.
func refuse(client *fake.Clientset, resource string) {
	client.PrependReactor("create", resource,
		func(clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Group: "authorization.k8s.io", Resource: resource},
				"", errors.New("not allowed"))
		})
}

func TestAccessReviewCarriesAnAllowedAnswerBack(t *testing.T) {
	client := fake.NewSimpleClientset()
	answerAccessReview(client, authzv1.SubjectAccessReviewStatus{
		Allowed: true,
		Reason:  `RBAC: allowed by ClusterRoleBinding "view-everywhere"`,
	}, nil)
	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.AccessReview(context.Background(), "dev", domain.AccessRequest{
		Verb:      "get",
		Resource:  "pods",
		Namespace: domain.NamespaceName("shop"),
	})
	if err != nil {
		t.Fatalf("AccessReview: %v", err)
	}

	if !outcome.Allowed {
		t.Error("an allowed review must arrive allowed")
	}
	if outcome.Denied {
		t.Error("an allowed review must not arrive denied")
	}
	// Verbatim: the API server's reason names the binding, which is the whole
	// value of showing it rather than paraphrasing it into "yes".
	if outcome.Reason != `RBAC: allowed by ClusterRoleBinding "view-everywhere"` {
		t.Errorf("reason must be carried verbatim, got %q", outcome.Reason)
	}
}

func TestAccessReviewCarriesADeniedAnswerAndItsReason(t *testing.T) {
	client := fake.NewSimpleClientset()
	answerAccessReview(client, authzv1.SubjectAccessReviewStatus{
		Allowed: false,
		Denied:  true,
		Reason:  "denied by webhook",
	}, nil)
	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.AccessReview(context.Background(), "dev", domain.AccessRequest{
		Verb: "delete", Resource: "pods", Namespace: domain.NamespaceName("kube-system"),
	})
	if err != nil {
		t.Fatalf("AccessReview: %v", err)
	}

	if outcome.Allowed {
		t.Error("a denied review must not arrive allowed")
	}
	if !outcome.Denied {
		t.Error("an explicit denial must arrive denied, which is not the same as merely not allowed")
	}
	if outcome.Reason != "denied by webhook" {
		t.Errorf("reason must be carried verbatim, got %q", outcome.Reason)
	}
}

// TestAccessReviewDistinguishesNoOpinionFromADenial pins the third answer the
// API has: an authorizer that neither allows nor denies leaves both flags
// false, and folding that into "denied" would claim a verdict nothing gave.
func TestAccessReviewDistinguishesNoOpinionFromADenial(t *testing.T) {
	client := fake.NewSimpleClientset()
	answerAccessReview(client, authzv1.SubjectAccessReviewStatus{
		Reason: "no RBAC policy matched",
	}, nil)
	adapter := newTestAdapter("dev", client)

	outcome, err := adapter.AccessReview(context.Background(), "dev", domain.AccessRequest{
		Verb: "get", Resource: "pods",
	})
	if err != nil {
		t.Fatalf("AccessReview: %v", err)
	}

	if outcome.Allowed || outcome.Denied {
		t.Errorf("no opinion must arrive as neither, got allowed=%v denied=%v",
			outcome.Allowed, outcome.Denied)
	}
}

func TestAccessReviewIsForbiddenWhenTheAccountMayNotAsk(t *testing.T) {
	client := fake.NewSimpleClientset()
	refuse(client, "selfsubjectaccessreviews")
	adapter := newTestAdapter("dev", client)

	_, err := adapter.AccessReview(context.Background(), "dev", domain.AccessRequest{
		Verb: "get", Resource: "pods",
	})
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

// TestAccessReviewChoosesTheReviewApiFromTheSubject pins which of the two
// APIs each question reaches. Asking about oneself through a
// SubjectAccessReview would need a permission most accounts do not hold, so
// getting this backwards turns an ordinary question into a refusal.
func TestAccessReviewChoosesTheReviewApiFromTheSubject(t *testing.T) {
	tests := []struct {
		name     string
		subject  domain.RBACSubject
		wantKind string
		wantUser string
		wantGrp  []string
	}{
		{
			name:     "no subject asks about the current account",
			subject:  domain.RBACSubject{},
			wantKind: "SelfSubjectAccessReview",
		},
		{
			name:     "a user is asked about by name",
			subject:  domain.RBACSubject{Kind: domain.SubjectUser, Name: "ada"},
			wantKind: "SubjectAccessReview",
			wantUser: "ada",
		},
		{
			name:     "a group is asked about as a group",
			subject:  domain.RBACSubject{Kind: domain.SubjectGroup, Name: "developers"},
			wantKind: "SubjectAccessReview",
			wantGrp:  []string{"developers"},
		},
		{
			// The authenticator presents a service account under this name
			// and the review API has no field for one, so this string is the
			// only way to ask the question.
			name: "a service account is asked about under its system: user name",
			subject: domain.RBACSubject{
				Kind:      domain.SubjectServiceAccount,
				Name:      "reader",
				Namespace: domain.NamespaceName("shop"),
			},
			wantKind: "SubjectAccessReview",
			wantUser: "system:serviceaccount:shop:reader",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			var sent runtime.Object
			answerAccessReview(client, authzv1.SubjectAccessReviewStatus{Allowed: true}, &sent)
			adapter := newTestAdapter("dev", client)

			if _, err := adapter.AccessReview(context.Background(), "dev", domain.AccessRequest{
				Subject: test.subject, Verb: "get", Resource: "pods",
			}); err != nil {
				t.Fatalf("AccessReview: %v", err)
			}

			switch review := sent.(type) {
			case *authzv1.SelfSubjectAccessReview:
				if test.wantKind != "SelfSubjectAccessReview" {
					t.Fatalf("want a %s, got a SelfSubjectAccessReview", test.wantKind)
				}
			case *authzv1.SubjectAccessReview:
				if test.wantKind != "SubjectAccessReview" {
					t.Fatalf("want a %s, got a SubjectAccessReview", test.wantKind)
				}
				if review.Spec.User != test.wantUser {
					t.Errorf("user: want %q, got %q", test.wantUser, review.Spec.User)
				}
				if len(review.Spec.Groups) != len(test.wantGrp) {
					t.Errorf("groups: want %v, got %v", test.wantGrp, review.Spec.Groups)
				}
			default:
				t.Fatalf("nothing recognisable was sent: %T", sent)
			}
		})
	}
}

func TestSubjectRulesSplitsResourceAndNonResourceRules(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "selfsubjectrulesreviews",
		func(action clientgotesting.Action) (bool, runtime.Object, error) {
			review := action.(clientgotesting.CreateAction).GetObject().(*authzv1.SelfSubjectRulesReview)
			answered := review.DeepCopy()
			answered.Status = authzv1.SubjectRulesReviewStatus{
				ResourceRules: []authzv1.ResourceRule{{
					Verbs:     []string{"get", "list"},
					APIGroups: []string{""},
					Resources: []string{"pods"},
				}},
				NonResourceRules: []authzv1.NonResourceRule{{
					Verbs:           []string{"get"},
					NonResourceURLs: []string{"/healthz"},
				}},
				Incomplete:      true,
				EvaluationError: "an authorizer did not answer",
			}
			return true, answered, nil
		})
	adapter := newTestAdapter("dev", client)

	review, err := adapter.SubjectRules(context.Background(), "dev", domain.NamespaceName("shop"))
	if err != nil {
		t.Fatalf("SubjectRules: %v", err)
	}

	if len(review.Resource) != 1 || review.Resource[0].Resources[0] != "pods" {
		t.Errorf("resource rules were not carried: %+v", review.Resource)
	}
	if len(review.NonResource) != 1 || review.NonResource[0].NonResourceURLs[0] != "/healthz" {
		t.Errorf("non-resource rules were not carried: %+v", review.NonResource)
	}
	// A partial answer that does not say so reads as a complete one, which is
	// the worst way for this pane to be wrong.
	if !review.Incomplete || review.IncompleteReason != "an authorizer did not answer" {
		t.Errorf("the API server's own incompleteness must be carried, got %+v", review)
	}
}

// TestSubjectRulesAsksAboutANamespaceEvenWhenTheTabSaysAll pins the one thing
// the caller cannot leave blank: the review API has no cluster-wide form, and
// an empty namespace is rejected rather than answered for everywhere.
func TestSubjectRulesAsksAboutANamespaceEvenWhenTheTabSaysAll(t *testing.T) {
	client := fake.NewSimpleClientset()
	var asked string
	client.PrependReactor("create", "selfsubjectrulesreviews",
		func(action clientgotesting.Action) (bool, runtime.Object, error) {
			review := action.(clientgotesting.CreateAction).GetObject().(*authzv1.SelfSubjectRulesReview)
			asked = review.Spec.Namespace
			return true, review.DeepCopy(), nil
		})
	adapter := newTestAdapter("dev", client)

	if _, err := adapter.SubjectRules(context.Background(), "dev", domain.NamespaceAll); err != nil {
		t.Fatalf("SubjectRules: %v", err)
	}

	if asked != domain.NamespaceDefault.String() {
		t.Errorf("want the default namespace, got %q", asked)
	}
}

func TestSubjectRulesIsForbiddenWhenTheAccountMayNotAsk(t *testing.T) {
	client := fake.NewSimpleClientset()
	refuse(client, "selfsubjectrulesreviews")
	adapter := newTestAdapter("dev", client)

	_, err := adapter.SubjectRules(context.Background(), "dev", domain.NamespaceName("shop"))
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}

func TestRoleRulesReadsBothScopes(t *testing.T) {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "editor", Namespace: "shop"},
		Rules: []rbacv1.PolicyRule{{
			Verbs:     []string{"get", "update"},
			APIGroups: []string{"apps"},
			Resources: []string{"deployments"},
		}},
	}
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "view"},
		Rules: []rbacv1.PolicyRule{{
			Verbs:           []string{"get"},
			NonResourceURLs: []string{"/healthz"},
		}},
	}
	adapter := newTestAdapter("dev", fake.NewSimpleClientset(role, clusterRole))

	namespaced, err := adapter.RoleRules(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeNamespace, Namespace: domain.NamespaceName("shop"), Name: "editor",
	})
	if err != nil {
		t.Fatalf("RoleRules (Role): %v", err)
	}
	if len(namespaced) != 1 || namespaced[0].Resources[0] != "deployments" {
		t.Errorf("a Role's rules were not carried: %+v", namespaced)
	}

	clusterWide, err := adapter.RoleRules(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "view",
	})
	if err != nil {
		t.Fatalf("RoleRules (ClusterRole): %v", err)
	}
	if len(clusterWide) != 1 || clusterWide[0].NonResourceURLs[0] != "/healthz" {
		t.Errorf("a ClusterRole's rules were not carried: %+v", clusterWide)
	}
}

func TestRoleRulesReportsAMissingRoleAsNotFound(t *testing.T) {
	adapter := newTestAdapter("dev", fake.NewSimpleClientset())

	_, err := adapter.RoleRules(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "nothing-here",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListBindingsReadsBothKindsWithTheirSubjects(t *testing.T) {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "view-everywhere"},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "view"},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "developers"},
			{Kind: "ServiceAccount", Name: "reader", Namespace: "shop"},
		},
	}
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "editors", Namespace: "shop"},
		RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "editor"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "ada"}},
	}
	adapter := newTestAdapter("dev", fake.NewSimpleClientset(clusterRoleBinding, roleBinding))

	bindings, err := adapter.ListBindings(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("want both bindings, got %d", len(bindings))
	}

	// Cluster-scoped first, so the panel reads outermost first.
	cluster := bindings[0]
	if cluster.Kind != "ClusterRoleBinding" || cluster.Name != "view-everywhere" {
		t.Errorf("want the ClusterRoleBinding first, got %+v", cluster)
	}
	if !cluster.Namespace.IsAll() {
		t.Errorf("a ClusterRoleBinding has no namespace, got %q", cluster.Namespace)
	}
	if len(cluster.Subjects) != 2 {
		t.Fatalf("want both subjects, got %+v", cluster.Subjects)
	}
	if cluster.Subjects[0].Kind != domain.SubjectGroup || !cluster.Subjects[0].Namespace.IsAll() {
		t.Errorf("a Group subject has no namespace, got %+v", cluster.Subjects[0])
	}
	if cluster.Subjects[1].Kind != domain.SubjectServiceAccount ||
		cluster.Subjects[1].Namespace != domain.NamespaceName("shop") {
		t.Errorf("a ServiceAccount subject keeps its namespace, got %+v", cluster.Subjects[1])
	}

	namespaced := bindings[1]
	if namespaced.Kind != "RoleBinding" || namespaced.Namespace != domain.NamespaceName("shop") {
		t.Errorf("want the RoleBinding with its namespace, got %+v", namespaced)
	}
	if namespaced.RoleRef.Kind != "Role" || namespaced.RoleRef.Name != "editor" {
		t.Errorf("the role reference must be carried, got %+v", namespaced.RoleRef)
	}
}

// TestListBindingsListsEveryNamespace pins the read that makes the reverse
// lookup honest: a ClusterRole is referenced by RoleBindings anywhere, so a
// list narrowed to one namespace would report it as bound to nobody.
func TestListBindingsListsEveryNamespace(t *testing.T) {
	client := fake.NewSimpleClientset(
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "here", Namespace: "shop"},
			RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "view"},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "there", Namespace: "billing"},
			RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "view"},
		},
	)
	adapter := newTestAdapter("dev", client)

	bindings, err := adapter.ListBindings(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListBindings: %v", err)
	}

	matched := domain.BindingsReferencing(
		domain.RoleTarget{Scope: domain.RoleScopeCluster, Name: "view"}, bindings)
	if len(matched) != 2 {
		t.Fatalf("want both namespaces' bindings, got %d", len(matched))
	}
}

func TestListBindingsIsForbiddenWhenTheAccountMayNotList(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "clusterrolebindings",
		func(clientgotesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{
					Group:    "rbac.authorization.k8s.io",
					Resource: "clusterrolebindings",
				}, "", errors.New("not allowed"))
		})
	adapter := newTestAdapter("dev", client)

	_, err := adapter.ListBindings(context.Background(), "dev")
	if !errors.Is(err, ports.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
}
