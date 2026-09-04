package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The RBAC explorer's use cases: what this kubeconfig may do here, whether a
// named action is permitted, and what one role reaches.
//
// The shape of this file is decided by one fact: BEING REFUSED IS AN ORDINARY
// ANSWER HERE, more so than anywhere else in the application. A
// SubjectAccessReview about somebody else is a privileged act, plenty of
// perfectly healthy accounts cannot list ClusterRoleBindings, and an operator
// investigating their own permissions is often exactly the person who has
// few. So a 403 becomes a domain.ReviewStatus on the result rather than an
// error on the call, and every pane says which of the three things happened —
// the same reasoning behind domain.MetricsStatus, and behind
// domain.ClusterReadStatus in the fleet.

// RBACServiceDeps are the collaborators the RBAC use cases need.
type RBACServiceDeps struct {
	// RBAC answers the review APIs and reads the binding graph. Required.
	RBAC ports.RBACPort
	// Registry tracks open connections. Required.
	Registry *Registry
	// Logger receives diagnostics. Optional; defaults to slog.Default.
	Logger *slog.Logger
}

// RBACService implements the RBAC explorer's use cases.
type RBACService struct {
	rbac     ports.RBACPort
	registry *Registry
	logger   *slog.Logger
}

// Compile-time proof that the service satisfies its inbound port.
var _ ports.RBACService = (*RBACService)(nil)

// NewRBACService validates deps and returns the service.
func NewRBACService(deps RBACServiceDeps) (*RBACService, error) {
	switch {
	case deps.RBAC == nil:
		return nil, errors.New("application: RBACService requires an RBACPort")
	case deps.Registry == nil:
		return nil, errors.New("application: RBACService requires a Registry")
	}

	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &RBACService{
		rbac:     deps.RBAC,
		registry: deps.Registry,
		logger:   logger.With(slog.String("service", "rbac")),
	}, nil
}

// SubjectRules answers what the current credentials may do in one namespace.
func (s *RBACService) SubjectRules(
	ctx context.Context,
	id domain.ClusterID,
	namespace domain.NamespaceName,
) (domain.SubjectRules, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.SubjectRules{}, fmt.Errorf("reviewing own rules: %w", err)
	}

	scope := namespace.OrDefault()

	review, err := s.rbac.SubjectRules(ctx, id, scope)
	if err != nil {
		// Cancellation is the caller navigating away, not an answer about
		// this account's permissions, so it fails the call rather than
		// becoming a status somebody would read as a refusal.
		if ctx.Err() != nil {
			return domain.SubjectRules{}, fmt.Errorf("reviewing own rules in %q of %q: %w", scope, id, err)
		}
		status := reviewStatusFor(err)
		return domain.SubjectRules{
			Namespace: scope,
			Status:    status,
			Refusal:   rulesRefusal(status),
		}, nil
	}

	return domain.SubjectRules{
		Namespace: scope,
		Status:    domain.ReviewAnswered,
		Review:    review,
	}, nil
}

// CanI answers one access review.
//
// The request is echoed onto the decision so a result cannot be read against
// a form that has since been edited: these answers are a sentence apart from
// each other — "yes to delete pods in kube-system" and "yes to get pods in
// default" look identical once the question has scrolled away.
func (s *RBACService) CanI(
	ctx context.Context,
	id domain.ClusterID,
	request domain.AccessRequest,
) (domain.AccessDecision, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.AccessDecision{}, fmt.Errorf("reviewing access: %w", err)
	}
	if request.Verb == "" || request.Resource == "" {
		return domain.AccessDecision{}, fmt.Errorf(
			"reviewing access: %w: a verb and a resource are both required",
			domain.ErrInvalidAccessRequest)
	}

	outcome, err := s.rbac.AccessReview(ctx, id, request)
	if err != nil {
		if ctx.Err() != nil {
			return domain.AccessDecision{}, fmt.Errorf("reviewing access in %q: %w", id, err)
		}
		status := reviewStatusFor(err)
		return domain.AccessDecision{
			Request: request,
			Status:  status,
			Refusal: accessRefusal(status, request.Subject),
		}, nil
	}

	// Logged at debug and by the QUESTION only. The subject is an object
	// name, and the verdict is about somebody's permissions; neither belongs
	// in a log file that outlives the window it was asked in.
	s.logger.DebugContext(ctx, "reviewed access",
		slog.String("cluster", id.String()),
		slog.String("verb", request.Verb),
		slog.String("resource", request.Resource),
		slog.Bool("self", request.Subject.IsZero()))

	return domain.AccessDecision{
		Request: request,
		Status:  domain.ReviewAnswered,
		Outcome: outcome,
	}, nil
}

// InspectRole reads one role, finds what references it, and assesses it.
//
// THE TWO READS RUN TOGETHER AND FAIL APART. They answer different halves of
// the panel and an account routinely holds one permission without the other,
// so neither branch returns an error into the group: each records its own
// status, and the group waits for both. Running them on the caller's context
// without cancelling on failure is deliberate — the fleet's own reads follow
// the same rule, and for the same reason.
func (s *RBACService) InspectRole(
	ctx context.Context,
	id domain.ClusterID,
	target domain.RoleTarget,
) (domain.RoleInspection, error) {
	if _, err := s.registry.Get(id); err != nil {
		return domain.RoleInspection{}, fmt.Errorf("inspecting role: %w", err)
	}
	if target.Name == "" {
		return domain.RoleInspection{}, fmt.Errorf(
			"inspecting role: %w: a role name is required", domain.ErrInvalidRoleTarget)
	}
	if target.Scope == domain.RoleScopeNamespace && target.Namespace.IsAll() {
		return domain.RoleInspection{}, fmt.Errorf(
			"inspecting role: %w: a Role is identified by its namespace as well as its name",
			domain.ErrInvalidRoleTarget)
	}

	// Each branch writes only its own locals, and the result is assembled
	// after Wait: two goroutines filling different fields of one struct would
	// work, and would be one refactor away from not.
	var (
		rules          []domain.PolicyRule
		bindings       []domain.RoleBindingRef
		roleStatus     = domain.ReviewAnswered
		roleRefused    string
		bindingStatus  = domain.ReviewAnswered
		bindingRefused string
		group          errgroup.Group
	)

	group.Go(func() error {
		read, err := s.rbac.RoleRules(ctx, id, target)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			roleStatus = reviewStatusFor(err)
			roleRefused = roleRefusal(roleStatus, target)
			return nil
		}
		rules = read
		return nil
	})

	group.Go(func() error {
		read, err := s.rbac.ListBindings(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			bindingStatus = reviewStatusFor(err)
			bindingRefused = bindingsRefusal(bindingStatus)
			return nil
		}
		bindings = domain.BindingsReferencing(target, read)
		return nil
	})

	if err := group.Wait(); err != nil {
		return domain.RoleInspection{}, fmt.Errorf(
			"inspecting %s %q of %q: %w", target.Kind(), target.Name, id, err)
	}

	inspection := domain.RoleInspection{
		Target:          target,
		Status:          roleStatus,
		Refusal:         roleRefused,
		Rules:           rules,
		BindingsStatus:  bindingStatus,
		BindingsRefusal: bindingRefused,
		Bindings:        bindings,
	}

	// The assessment is over what was actually read. A refused binding list
	// contributes no bindings rather than an empty one, which is the same
	// thing to AssessRole and deliberately so: a flag about bindings nobody
	// could list would be a claim about something unread.
	inspection.Findings = domain.AssessRole(domain.RoleReview{
		Scope:    target.Scope,
		Name:     target.Name,
		Rules:    rules,
		Bindings: bindings,
	})

	s.logger.DebugContext(ctx, "inspected role",
		slog.String("cluster", id.String()),
		slog.String("kind", target.Kind()),
		slog.Int("rules", len(rules)),
		slog.Int("bindings", len(bindings)),
		slog.Int("findings", len(inspection.Findings)))

	return inspection, nil
}

// reviewStatusFor maps a failed review onto what to tell the operator.
//
// The adapter has already done the hard part — classify turns a 403 into
// ErrForbidden and a dead cluster into ErrUnreachable — so this only chooses
// which sentence those deserve. Modelled on metricsStatusFor in overview.go,
// and kept beside its callers for the same reason that one is.
func reviewStatusFor(err error) domain.ReviewStatus {
	switch {
	case errors.Is(err, ports.ErrForbidden), errors.Is(err, ports.ErrUnauthenticated):
		return domain.ReviewForbidden
	case errors.Is(err, ports.ErrNotFound):
		return domain.ReviewAbsent
	default:
		return domain.ReviewFailed
	}
}

// rulesRefusal is the sentence shown in place of a rules review.
func rulesRefusal(status domain.ReviewStatus) string {
	switch status {
	case domain.ReviewForbidden:
		return "Your account may not ask this. Listing your own permissions needs `create` on " +
			"selfsubjectrulesreviews, which most clusters grant to everyone and some do not."
	case domain.ReviewAbsent:
		return "This cluster does not serve the rules review API, so there is nothing to read."
	default:
		return "The review could not be read. The cluster may be unreachable; try again."
	}
}

// accessRefusal is the sentence shown in place of an access decision.
//
// A named subject gets a different sentence from an unnamed one, because the
// two questions need different permissions and only one of them is normally
// held: asking about yourself is something every authenticated account may
// do, and asking about somebody else is an administrator's act.
func accessRefusal(status domain.ReviewStatus, subject domain.RBACSubject) string {
	switch status {
	case domain.ReviewForbidden:
		if subject.IsZero() {
			return "Your account may not ask this. Checking your own access needs `create` on " +
				"selfsubjectaccessreviews."
		}
		return "Your account may not ask this. Checking another subject's access needs `create` on " +
			"subjectaccessreviews, which is a privileged permission most accounts do not hold."
	case domain.ReviewAbsent:
		return "This cluster does not serve the access review API, so there is nothing to ask."
	default:
		return "The review could not be read. The cluster may be unreachable; try again."
	}
}

// roleRefusal is the sentence shown in place of a role's rules.
func roleRefusal(status domain.ReviewStatus, target domain.RoleTarget) string {
	switch status {
	case domain.ReviewForbidden:
		return "Your account may not read this " + target.Kind() + "."
	case domain.ReviewAbsent:
		return "No " + target.Kind() + " by that name exists here."
	default:
		return "The " + target.Kind() + " could not be read. The cluster may be unreachable; try again."
	}
}

// bindingsRefusal is the sentence shown in place of the binding lists.
func bindingsRefusal(status domain.ReviewStatus) string {
	switch status {
	case domain.ReviewForbidden:
		return "Your account may not list bindings, so who holds this role cannot be shown. " +
			"It needs `list` on rolebindings and clusterrolebindings across the cluster."
	case domain.ReviewAbsent:
		return "This cluster serves no RBAC bindings to list."
	default:
		return "The bindings could not be listed. The cluster may be unreachable; try again."
	}
}
