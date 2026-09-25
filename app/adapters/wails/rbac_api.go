package wails

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// RBACAPI exposes the RBAC explorer.
//
// EVERY METHOD IS A READ AND EVERY METHOD IS ON DEMAND. Nothing here is
// called by the refresh tick, and nothing here is cached: an allow or deny
// decision shown from a previous instant could report a permission that has
// since been revoked as still granted, which is the one answer this feature
// must never give.
type RBACAPI struct {
	rbac ports.RBACService
	app  *App
	// logger receives the operation and the error, never a subject name and
	// never a verdict — see apiError.
	logger *slog.Logger
}

// NewRBACAPI returns the bound RBAC API.
func NewRBACAPI(rbac ports.RBACService, app *App, logger *slog.Logger) (*RBACAPI, error) {
	switch {
	case rbac == nil:
		return nil, errors.New("wails: RBACAPI requires an RBACService")
	case app == nil:
		return nil, errors.New("wails: RBACAPI requires an App")
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &RBACAPI{
		rbac:   rbac,
		app:    app,
		logger: logger.With(slog.String("api", "rbac")),
	}, nil
}

// SubjectRules answers what the current credentials may do in one namespace.
//
// ONE REQUEST for the whole answer — SelfSubjectRulesReview enumerates every
// rule that applies, so this is not one access check per verb per resource.
func (r *RBACAPI) SubjectRules(clusterID, namespace string) (SubjectRules, error) {
	ctx, cancel := r.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return SubjectRules{}, apiError(r.logger, "SubjectRules", err)
	}

	name, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return SubjectRules{}, apiError(r.logger, "SubjectRules", err)
	}

	rules, err := r.rbac.SubjectRules(ctx, id, name)
	if err != nil {
		return SubjectRules{}, apiError(r.logger, "SubjectRules", err)
	}

	return toSubjectRules(rules), nil
}

// CanI answers one access review, for the current account or a named subject.
//
// The API server decides. Its own allowed, denied and reason cross this
// boundary untouched, and nothing in PodSteer evaluates a rule to second-guess
// them.
func (r *RBACAPI) CanI(clusterID string, request AccessRequest) (AccessDecision, error) {
	ctx, cancel := r.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return AccessDecision{}, apiError(r.logger, "CanI", err)
	}

	namespace, err := domain.NewNamespaceName(request.Namespace)
	if err != nil {
		return AccessDecision{}, apiError(r.logger, "CanI", err)
	}

	subject, err := fromRBACSubject(RBACSubject{
		Kind:      request.SubjectKind,
		Name:      request.SubjectName,
		Namespace: request.SubjectNamespace,
	})
	if err != nil {
		return AccessDecision{}, apiError(r.logger, "CanI", err)
	}

	decision, err := r.rbac.CanI(ctx, id, domain.AccessRequest{
		Subject:     subject,
		Verb:        request.Verb,
		Group:       request.Group,
		Resource:    request.Resource,
		Subresource: request.Subresource,
		Namespace:   namespace,
		Name:        request.Name,
	})
	if err != nil {
		return AccessDecision{}, apiError(r.logger, "CanI", err)
	}

	return toAccessDecision(decision), nil
}

// InspectRole reads one Role or ClusterRole, finds the bindings referencing
// it, and assesses what its rules permit.
//
// scope is "cluster" or "namespace". Called when the panel is opened, and not
// on any tick: it costs three requests, two of them cluster-wide lists.
func (r *RBACAPI) InspectRole(clusterID, scope, namespace, name string) (RoleInspection, error) {
	ctx, cancel := r.app.requestContext()
	defer cancel()

	id, err := domain.NewClusterID(clusterID)
	if err != nil {
		return RoleInspection{}, apiError(r.logger, "InspectRole", err)
	}

	roleScope, err := fromRoleScope(scope)
	if err != nil {
		return RoleInspection{}, apiError(r.logger, "InspectRole", err)
	}

	ns, err := domain.NewNamespaceName(namespace)
	if err != nil {
		return RoleInspection{}, apiError(r.logger, "InspectRole", err)
	}

	inspection, err := r.rbac.InspectRole(ctx, id, domain.RoleTarget{
		Scope:     roleScope,
		Namespace: ns,
		Name:      name,
	})
	if err != nil {
		return RoleInspection{}, apiError(r.logger, "InspectRole", err)
	}

	return toRoleInspection(inspection), nil
}

// fromRBACSubject validates a subject the frontend typed.
//
// An empty kind AND an empty name is the current account, which is a
// different review API rather than a missing field. Anything else must name
// one of the three kinds Kubernetes has: a typo silently becomes a
// SubjectAccessReview about a user nobody has, which answers "no" and reads
// as a real denial.
func fromRBACSubject(subject RBACSubject) (domain.RBACSubject, error) {
	if subject.Kind == "" && subject.Name == "" {
		return domain.RBACSubject{}, nil
	}

	kind := domain.SubjectKind(subject.Kind)
	switch kind {
	case domain.SubjectUser, domain.SubjectGroup, domain.SubjectServiceAccount:
	default:
		return domain.RBACSubject{}, fmt.Errorf(
			"%w: subject kind %q is not User, Group or ServiceAccount",
			domain.ErrInvalidAccessRequest, subject.Kind)
	}
	if subject.Name == "" {
		return domain.RBACSubject{}, fmt.Errorf(
			"%w: a %s subject needs a name", domain.ErrInvalidAccessRequest, kind)
	}

	namespace, err := domain.NewNamespaceName(subject.Namespace)
	if err != nil {
		return domain.RBACSubject{}, err
	}

	return domain.RBACSubject{Kind: kind, Name: subject.Name, Namespace: namespace}, nil
}

// fromRoleScope validates the scope the frontend named.
//
// Refused rather than defaulted: guessing "namespace" for an unrecognised
// value would read a Role where a ClusterRole was meant, and report a widely
// bound cluster role as nonexistent.
func fromRoleScope(scope string) (domain.RoleScope, error) {
	switch domain.RoleScope(scope) {
	case domain.RoleScopeCluster:
		return domain.RoleScopeCluster, nil
	case domain.RoleScopeNamespace:
		return domain.RoleScopeNamespace, nil
	default:
		return "", fmt.Errorf(
			"%w: scope %q is neither %q nor %q",
			domain.ErrInvalidRoleTarget, scope, domain.RoleScopeCluster, domain.RoleScopeNamespace)
	}
}
