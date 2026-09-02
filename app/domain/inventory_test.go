package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func kind(name, title string, namespaced bool, category domain.ResourceCategory) domain.ResourceKind {
	return domain.ResourceKind{
		Version: "v1", Resource: title, Kind: name, Title: title,
		Namespaced: namespaced, Category: category,
	}
}

func TestCountableKindsLeavesOutWhatCannotBeCountedPerNamespace(t *testing.T) {
	kinds := []domain.ResourceKind{
		kind("Pod", "Pods", true, domain.CategoryWorkloads),
		kind("Node", "Nodes", false, domain.CategoryCluster),
		kind("Event", "Events", true, domain.CategoryCluster),
		kind("Certificate", "Certificates", true, domain.CategoryCustomResources),
		kind("ConfigMap", "ConfigMaps", true, domain.CategoryConfig),
	}

	got := domain.CountableKinds(kinds)

	if len(got) != 2 {
		t.Fatalf("counting %d kinds, want Pods and ConfigMaps only: %+v", len(got), got)
	}
	if got[0].Kind != "Pod" || got[1].Kind != "ConfigMap" {
		t.Fatalf("kept the wrong kinds: %q and %q", got[0].Kind, got[1].Kind)
	}
}

func TestEmptyKindsAreCountedButNotListed(t *testing.T) {
	// Fourteen zeroes would bury the two counts somebody opened the panel
	// for; "and 14 hold nothing" is the same information without the wall.
	inventory := domain.NewNamespaceInventory("web", []domain.ResourceCount{
		{Kind: kind("Pod", "Pods", true, domain.CategoryWorkloads), Count: 29},
		{Kind: kind("Job", "Jobs", true, domain.CategoryWorkloads), Count: 0},
		{Kind: kind("ConfigMap", "ConfigMaps", true, domain.CategoryConfig), Count: 7},
		{Kind: kind("Ingress", "Ingresses", true, domain.CategoryNetwork), Count: 0},
	})

	if len(inventory.Counts) != 2 {
		t.Fatalf("listed %d kinds, want the 2 that hold something", len(inventory.Counts))
	}
	if inventory.Empty != 2 {
		t.Fatalf("Empty = %d, want 2", inventory.Empty)
	}
	if inventory.Total != 36 {
		t.Fatalf("Total = %d, want 36", inventory.Total)
	}
	// Largest first: the question is what the namespace is mostly made of.
	if inventory.Counts[0].Kind.Kind != "Pod" {
		t.Fatalf("led with %q, want Pod", inventory.Counts[0].Kind.Kind)
	}
}

func TestARefusedCountIsKeptAndIsNotZero(t *testing.T) {
	// THE FAILURE THIS GUARDS. An account with `list pods` and not `list
	// secrets` must not be told the namespace holds no secrets — it holds an
	// unknown number, and the difference is the whole point of asking.
	inventory := domain.NewNamespaceInventory("web", []domain.ResourceCount{
		{Kind: kind("Pod", "Pods", true, domain.CategoryWorkloads), Count: 29},
		{
			Kind:       kind("Secret", "Secrets", true, domain.CategoryConfig),
			Unreadable: "not permitted to list secrets",
		},
	})

	if inventory.Unreadable != 1 {
		t.Fatalf("Unreadable = %d, want 1", inventory.Unreadable)
	}
	if inventory.Empty != 0 {
		t.Fatalf("Empty = %d — a refusal was counted as an empty kind", inventory.Empty)
	}
	if inventory.Total != 29 {
		t.Fatalf("Total = %d, want 29 — a refusal must contribute nothing", inventory.Total)
	}
	// Last, so the counts lead and the gap in them is still visible.
	last := inventory.Counts[len(inventory.Counts)-1]
	if last.Kind.Kind != "Secret" || last.Known() {
		t.Fatalf("refusal did not sort last: %+v", inventory.Counts)
	}
	if inventory.IsEmpty() {
		t.Fatal("IsEmpty on a namespace whose contents were partly refused")
	}
}

func TestTiesBreakOnTitleSoTheListDoesNotReshuffle(t *testing.T) {
	inventory := domain.NewNamespaceInventory("web", []domain.ResourceCount{
		{Kind: kind("Service", "Services", true, domain.CategoryNetwork), Count: 3},
		{Kind: kind("ConfigMap", "ConfigMaps", true, domain.CategoryConfig), Count: 3},
	})

	if inventory.Counts[0].Kind.Title != "ConfigMaps" {
		t.Fatalf("led with %q, want ConfigMaps", inventory.Counts[0].Kind.Title)
	}
}
