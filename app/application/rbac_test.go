package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/application"
	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// fakeRBAC stands in for the review APIs and the binding lists. Each answer
// has an error beside it so a test can refuse one half of a panel and leave
// the other answering, which is the shape this service exists to handle.
type fakeRBAC struct {
	rules       domain.RulesReview
	rulesErr    error
	outcome     domain.AccessOutcome
	outcomeErr  error
	roleRules   []domain.PolicyRule
	roleErr     error
	bindings    []domain.RoleBindingRef
	bindingsErr error

	// askedNamespace records what SubjectRules was asked about, since the
	// service normalises "all namespaces" before the port ever sees it.
	askedNamespace domain.NamespaceName
}

var _ ports.RBACPort = (*fakeRBAC)(nil)

func (f *fakeRBAC) SubjectRules(_ context.Context, _ domain.ClusterID, namespace domain.NamespaceName) (domain.RulesReview, error) {
	f.askedNamespace = namespace
	return f.rules, f.rulesErr
}

func (f *fakeRBAC) AccessReview(context.Context, domain.ClusterID, domain.AccessRequest) (domain.AccessOutcome, error) {
	return f.outcome, f.outcomeErr
}

func (f *fakeRBAC) RoleRules(context.Context, domain.ClusterID, domain.RoleTarget) ([]domain.PolicyRule, error) {
	return f.roleRules, f.roleErr
}

func (f *fakeRBAC) ListBindings(context.Context, domain.ClusterID) ([]domain.RoleBindingRef, error) {
	return f.bindings, f.bindingsErr
}

func newRBACService(t *testing.T, rbac *fakeRBAC) *application.RBACService {
	t.Helper()

	registry := application.NewRegistry()
	registry.Open(mustCluster(t, "dev", true))

	service, err := application.NewRBACService(application.RBACServiceDeps{
		RBAC:     rbac,
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("NewRBACService() error = %v", err)
	}
	return service
}

// forbidden is the error the adapter's classifier produces for a 403.
func forbidden(op string) error {
	return fmt.Errorf("%s: %w", op, ports.ErrForbidden)
}

// TestReviewRefusalsAreAnswersRatherThanErrors is the rule the whole feature
// rests on: an account that may not ask a review is the ordinary case here,
// and reporting it as a failed call would leave a broken pane where a
// sentence belongs.
func TestReviewRefusalsAreAnswersRatherThanErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus domain.ReviewStatus
		wantSays   string
	}{
		{
			name:       "a refusal",
			err:        forbidden("reviewing"),
			wantStatus: domain.ReviewForbidden,
			wantSays:   "may not ask this",
		},
		{
			name:       "an API the cluster does not serve",
			err:        fmt.Errorf("reviewing: %w", ports.ErrNotFound),
			wantStatus: domain.ReviewAbsent,
			wantSays:   "does not serve",
		},
		{
			name:       "a transient failure",
			err:        fmt.Errorf("reviewing: %w", ports.ErrUnreachable),
			wantStatus: domain.ReviewFailed,
			wantSays:   "try again",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := newRBACService(t, &fakeRBAC{rulesErr: test.err, outcomeErr: test.err})

			rules, err := service.SubjectRules(context.Background(), "dev", "shop")
			if err != nil {
				t.Fatalf("SubjectRules() error = %v, want a status instead", err)
			}
			if rules.Status != test.wantStatus {
				t.Errorf("SubjectRules status = %q, want %q", rules.Status, test.wantStatus)
			}
			if !strings.Contains(rules.Refusal, test.wantSays) {
				t.Errorf("SubjectRules refusal = %q, want it to mention %q", rules.Refusal, test.wantSays)
			}

			decision, err := service.CanI(context.Background(), "dev", domain.AccessRequest{
				Verb: "get", Resource: "pods",
			})
			if err != nil {
				t.Fatalf("CanI() error = %v, want a status instead", err)
			}
			if decision.Status != test.wantStatus {
				t.Errorf("CanI status = %q, want %q", decision.Status, test.wantStatus)
			}
		})
	}
}

// TestCanIQuotesTheApiServersOwnVerdict pins that nothing between the API
// server and the screen reinterprets the answer.
func TestCanIQuotesTheApiServersOwnVerdict(t *testing.T) {
	t.Parallel()

	rbac := &fakeRBAC{outcome: domain.AccessOutcome{
		Denied: true,
		Reason: `RBAC: denied by ClusterRole "view"`,
	}}
	service := newRBACService(t, rbac)

	decision, err := service.CanI(context.Background(), "dev", domain.AccessRequest{
		Verb: "delete", Resource: "pods", Namespace: "kube-system",
	})
	if err != nil {
		t.Fatalf("CanI() error = %v", err)
	}

	if decision.Status != domain.ReviewAnswered {
		t.Fatalf("status = %q, want %q", decision.Status, domain.ReviewAnswered)
	}
	if !decision.Outcome.Denied || decision.Outcome.Allowed {
		t.Errorf("verdict changed on the way through: %+v", decision.Outcome)
	}
	if decision.Outcome.Reason != `RBAC: denied by ClusterRole "view"` {
		t.Errorf("reason = %q, want it verbatim", decision.Outcome.Reason)
	}
	// Echoed, so a decision cannot be read against a question the form has
	// since moved on from.
	if decision.Request.Verb != "delete" || decision.Request.Namespace != "kube-system" {
		t.Errorf("the question must come back with the answer, got %+v", decision.Request)
	}
}

// TestCanIRefusesAQuestionWithNothingInIt pins the local check: the API
// server answers a malformed request with a flat "no", which reads as a
// denial of something rather than as the empty question it is.
func TestCanIRefusesAQuestionWithNothingInIt(t *testing.T) {
	t.Parallel()

	service := newRBACService(t, &fakeRBAC{})

	if _, err := service.CanI(context.Background(), "dev", domain.AccessRequest{Resource: "pods"}); !errors.Is(err, domain.ErrInvalidAccessRequest) {
		t.Errorf("a request with no verb: want ErrInvalidAccessRequest, got %v", err)
	}
	if _, err := service.CanI(context.Background(), "dev", domain.AccessRequest{Verb: "get"}); !errors.Is(err, domain.ErrInvalidAccessRequest) {
		t.Errorf("a request with nothing to act on: want ErrInvalidAccessRequest, got %v", err)
	}
}

// TestSubjectRulesNamesANamespaceEvenWhenTheTabDoesNot pins the
// normalisation, since the review API has no cluster-wide form.
func TestSubjectRulesNamesANamespaceEvenWhenTheTabDoesNot(t *testing.T) {
	t.Parallel()

	rbac := &fakeRBAC{}
	service := newRBACService(t, rbac)

	rules, err := service.SubjectRules(context.Background(), "dev", domain.NamespaceAll)
	if err != nil {
		t.Fatalf("SubjectRules() error = %v", err)
	}

	if rbac.askedNamespace != domain.NamespaceDefault {
		t.Errorf("asked about %q, want %q", rbac.askedNamespace, domain.NamespaceDefault)
	}
	// And the answer says which namespace it is about, so the pane cannot
	// claim to describe one the review never covered.
	if rules.Namespace != domain.NamespaceDefault {
		t.Errorf("answer names %q, want %q", rules.Namespace, domain.NamespaceDefault)
	}
}

// TestInspectRoleReportsTheRoleAndItsBindingsSeparately is the reason those
// two statuses exist: an account may read a ClusterRole and not be allowed to
// list the bindings to it, and one refusal must not blank the other half.
func TestInspectRoleReportsTheRoleAndItsBindingsSeparately(t *testing.T) {
	t.Parallel()

	rbac := &fakeRBAC{
		roleRules: []domain.PolicyRule{{
			Verbs: []string{"impersonate"}, APIGroups: []string{""}, Resources: []string{"users"},
		}},
		bindingsErr: forbidden("listing bindings"),
	}
	service := newRBACService(t, rbac)

	inspection, err := service.InspectRole(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "impersonator",
	})
	if err != nil {
		t.Fatalf("InspectRole() error = %v", err)
	}

	if inspection.Status != domain.ReviewAnswered {
		t.Errorf("the role was read; status = %q", inspection.Status)
	}
	if inspection.BindingsStatus != domain.ReviewForbidden {
		t.Errorf("the bindings were refused; status = %q", inspection.BindingsStatus)
	}
	if inspection.BindingsRefusal == "" {
		t.Error("a refused binding list must say so")
	}
	// The rules half still produced its verdict.
	if len(inspection.Findings) == 0 {
		t.Error("a refused binding list must not suppress the findings from the rules")
	}
}

// TestInspectRoleAssessesOnlyWhatWasRead pins that a refused binding list
// produces no binding finding rather than a claim about bindings nobody
// could see.
func TestInspectRoleAssessesOnlyWhatWasRead(t *testing.T) {
	t.Parallel()

	rbac := &fakeRBAC{bindingsErr: forbidden("listing bindings")}
	service := newRBACService(t, rbac)

	inspection, err := service.InspectRole(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "cluster-admin",
	})
	if err != nil {
		t.Fatalf("InspectRole() error = %v", err)
	}

	for _, finding := range inspection.Findings {
		if finding.ID == "rbac:binds-cluster-admin" {
			t.Fatal("a binding finding was raised from bindings nobody could list")
		}
	}
}

// TestInspectRoleFiltersBindingsToTheRoleAsked pins that the reverse lookup
// runs — the port hands back every binding in the cluster, and only the ones
// referencing this role belong on the panel.
func TestInspectRoleFiltersBindingsToTheRoleAsked(t *testing.T) {
	t.Parallel()

	rbac := &fakeRBAC{bindings: []domain.RoleBindingRef{
		{
			Kind:     "ClusterRoleBinding",
			Name:     "wanted",
			RoleRef:  domain.RoleRef{Kind: "ClusterRole", Name: "view"},
			Subjects: []domain.RBACSubject{{Kind: domain.SubjectUser, Name: "ada"}},
		},
		{
			Kind:    "ClusterRoleBinding",
			Name:    "unwanted",
			RoleRef: domain.RoleRef{Kind: "ClusterRole", Name: "edit"},
		},
	}}
	service := newRBACService(t, rbac)

	inspection, err := service.InspectRole(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "view",
	})
	if err != nil {
		t.Fatalf("InspectRole() error = %v", err)
	}

	if len(inspection.Bindings) != 1 || inspection.Bindings[0].Name != "wanted" {
		t.Fatalf("want only the binding referencing this role, got %+v", inspection.Bindings)
	}
}

// TestInspectRoleRefusesATargetThatNamesNoObject pins the local check: a Role
// is identified by its namespace as well as its name, so a blank one would
// read some other namespace's identically named Role.
func TestInspectRoleRefusesATargetThatNamesNoObject(t *testing.T) {
	t.Parallel()

	service := newRBACService(t, &fakeRBAC{})

	_, err := service.InspectRole(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeNamespace, Name: "editor",
	})
	if !errors.Is(err, domain.ErrInvalidRoleTarget) {
		t.Errorf("a Role with no namespace: want ErrInvalidRoleTarget, got %v", err)
	}

	_, err = service.InspectRole(context.Background(), "dev", domain.RoleTarget{
		Scope: domain.RoleScopeCluster,
	})
	if !errors.Is(err, domain.ErrInvalidRoleTarget) {
		t.Errorf("a role with no name: want ErrInvalidRoleTarget, got %v", err)
	}
}

// TestRBACCallsRequireAConnectedCluster pins that every entry point goes
// through the registry, the way every other read does.
func TestRBACCallsRequireAConnectedCluster(t *testing.T) {
	t.Parallel()

	service := newRBACService(t, &fakeRBAC{})

	if _, err := service.SubjectRules(context.Background(), "closed", "shop"); err == nil {
		t.Error("SubjectRules on an unopened cluster must fail")
	}
	if _, err := service.CanI(context.Background(), "closed", domain.AccessRequest{
		Verb: "get", Resource: "pods",
	}); err == nil {
		t.Error("CanI on an unopened cluster must fail")
	}
	if _, err := service.InspectRole(context.Background(), "closed", domain.RoleTarget{
		Scope: domain.RoleScopeCluster, Name: "view",
	}); err == nil {
		t.Error("InspectRole on an unopened cluster must fail")
	}
}
