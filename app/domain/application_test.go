package domain_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func labelled(kind, namespace, instance string, extra map[string]string) domain.ApplicationObject {
	labels := map[string]string{}
	if instance != "" {
		labels[domain.LabelInstance] = instance
	}
	for key, value := range extra {
		labels[key] = value
	}
	return domain.ApplicationObject{
		Kind:      kind,
		Namespace: domain.NamespaceName(namespace),
		Labels:    labels,
	}
}

func TestObjectsAreGroupedByTheirInstanceLabel(t *testing.T) {
	inventory := domain.NewApplicationInventory([]domain.ApplicationObject{
		labelled("Deployment", "development", "bill-registry-service", map[string]string{
			domain.LabelPartOf: "parlitrack",
		}),
		labelled("Service", "development", "bill-registry-service", nil),
		labelled("Ingress", "development", "bill-registry-service", nil),
		labelled("Deployment", "development", "iam-service", nil),
	})

	if len(inventory.Applications) != 2 {
		t.Fatalf("found %d applications, want 2", len(inventory.Applications))
	}

	bill := inventory.Applications[0]
	if bill.Instance != "bill-registry-service" {
		t.Fatalf("led with %q", bill.Instance)
	}
	if bill.Objects != 3 {
		t.Fatalf("bill-registry-service holds %d objects, want 3", bill.Objects)
	}
	if bill.PartOf != "parlitrack" {
		t.Fatalf("part-of read as %q", bill.PartOf)
	}
}

func TestWhatIsUnlabelledIsCountedRatherThanDropped(t *testing.T) {
	// THE HONESTY THIS TURNS ON. The labels are a convention, not a
	// guarantee: a chart that does not set them is invisible to any grouping
	// built on them. A view that silently omits a third of a namespace is
	// worse than no view, because it looks complete.
	inventory := domain.NewApplicationInventory([]domain.ApplicationObject{
		labelled("Deployment", "development", "web", nil),
		labelled("Deployment", "development", "", nil),
		labelled("Service", "development", "", nil),
	})

	if len(inventory.Applications) != 1 {
		t.Fatalf("found %d applications, want 1", len(inventory.Applications))
	}
	if inventory.Unlabelled != 2 {
		t.Fatalf("Unlabelled = %d, want the 2 objects that say nothing", inventory.Unlabelled)
	}
}

func TestAnInstanceDeployedTwiceIsTwoApplications(t *testing.T) {
	// Two copies with separate lifecycles. Merging them would report one
	// thing where an operator has two to restart.
	inventory := domain.NewApplicationInventory([]domain.ApplicationObject{
		labelled("Deployment", "development", "web", nil),
		labelled("Deployment", "staging", "web", nil),
	})

	if len(inventory.Applications) != 2 {
		t.Fatalf("found %d applications, want one per namespace", len(inventory.Applications))
	}
	if inventory.Applications[0].Namespace == inventory.Applications[1].Namespace {
		t.Fatal("the two copies were merged into one namespace")
	}
}

func TestMembersLeadWithWhatThereIsMostOf(t *testing.T) {
	inventory := domain.NewApplicationInventory([]domain.ApplicationObject{
		labelled("Pod", "development", "web", nil),
		labelled("Pod", "development", "web", nil),
		labelled("Pod", "development", "web", nil),
		labelled("Service", "development", "web", nil),
	})

	members := inventory.Applications[0].Members
	if members[0].Kind != "Pod" || members[0].Count != 3 {
		t.Fatalf("led with %+v, want 3 Pods", members[0])
	}
}

func TestADescriptiveLabelIsTakenFromWhateverCarriesIt(t *testing.T) {
	// Objects of one application can disagree — a Service labelled with a
	// version its Deployment has moved past. Taking the first value seen is
	// at least a value something carries, where merging would invent one.
	inventory := domain.NewApplicationInventory([]domain.ApplicationObject{
		labelled("Service", "development", "web", nil),
		labelled("Deployment", "development", "web", map[string]string{
			domain.LabelVersion:   "v1.2.3",
			domain.LabelManagedBy: "argocd",
		}),
	})

	application := inventory.Applications[0]
	if application.Version != "v1.2.3" || application.ManagedBy != "argocd" {
		t.Fatalf("descriptive labels lost: %+v", application)
	}
}
