package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestAGroupIsNamedForItsProjectAndNeverForAProduct(t *testing.T) {
	// argoproj.io publishes Argo CD, Argo Workflows, Argo Rollouts and Argo
	// Events. A heading claiming "Argo CD" on a cluster running only Argo
	// Workflows asserts that a controller is installed which is not, and
	// teaches an operator to distrust every other heading here.
	if owner := domain.GroupOwner("argoproj.io"); owner != "Argo" {
		t.Fatalf("argoproj.io named %q; it publishes four products", owner)
	}
}

func TestAnUncuratedGroupIsShownVerbatim(t *testing.T) {
	// No stripping of suffixes and no title-casing. "example.crossplane.io"
	// prettified into "Example" is a name nobody chose and nothing can be
	// searched for, while the raw group is exactly what kubectl prints.
	for _, group := range []string{"widgets.acme.example", "internal.corp"} {
		if owner := domain.GroupOwner(group); owner != group {
			t.Fatalf("%q was renamed to %q rather than shown as itself", group, owner)
		}
	}
}

func TestASubgroupFollowsItsParent(t *testing.T) {
	// A subgroup of something curated was published by the same project, so
	// following the parent is safe in a way inventing a name is not.
	if owner := domain.GroupOwner("acme.cert-manager.io"); owner != "cert-manager" {
		t.Fatalf("a cert-manager subgroup named %q", owner)
	}
	if owner := domain.GroupOwner("stable.example.com"); owner != "stable.example.com" {
		t.Fatalf("an uncurated subgroup was given its parent's name: %q", owner)
	}
}

func TestSeveralGroupsOfOneProjectShareAHeading(t *testing.T) {
	// Flux publishes five groups and is one thing in a navigator. Istio and
	// Crossplane are the same shape.
	for _, group := range []string{
		"kustomize.toolkit.fluxcd.io",
		"source.toolkit.fluxcd.io",
		"helm.toolkit.fluxcd.io",
	} {
		if owner := domain.GroupOwner(group); owner != "Flux" {
			t.Fatalf("%q named %q, splitting one project across headings", group, owner)
		}
	}
}
