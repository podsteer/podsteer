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
	}, nil, false)

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
	}, nil, false)

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
	}, nil, false)

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
	}, nil, false)

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
	}, nil, false)

	application := inventory.Applications[0]
	if application.Version != "v1.2.3" || application.ManagedBy != "argocd" {
		t.Fatalf("descriptive labels lost: %+v", application)
	}
}

func TestAnApplicationIsMeteredFromItsOwnPods(t *testing.T) {
	// An application has no usage of its own, for the reason a controller
	// does not: the consumption belongs to whatever pods carry its label at
	// this moment. Pods of another application must not be charged to it.
	mine, err := domain.NewPod(domain.PodSpec{
		Name: "web-1", Namespace: "development", ClusterID: "dev",
		Phase: domain.PodPhaseRunning, NodeName: "node-1",
		Labels:     map[string]string{domain.LabelInstance: "web"},
		Containers: []domain.Container{{Name: "app", Requests: domain.Resources{CPUMilli: 100}}},
		Usage:      domain.NewMetrics(30, 0),
	})
	if err != nil {
		t.Fatalf("building pod: %v", err)
	}
	theirs, err := domain.NewPod(domain.PodSpec{
		Name: "api-1", Namespace: "development", ClusterID: "dev",
		Phase: domain.PodPhaseRunning, NodeName: "node-1",
		Labels:     map[string]string{domain.LabelInstance: "api"},
		Containers: []domain.Container{{Name: "app", Requests: domain.Resources{CPUMilli: 500}}},
		Usage:      domain.NewMetrics(400, 0),
	})
	if err != nil {
		t.Fatalf("building pod: %v", err)
	}

	inventory := domain.NewApplicationInventory(
		[]domain.ApplicationObject{
			labelled("Pod", "development", "web", nil),
			labelled("Pod", "development", "api", nil),
		},
		[]domain.Pod{mine, theirs},
		true,
	)

	web := inventory.Applications[1]
	if web.Instance != "web" {
		t.Fatalf("expected web second, got %q", web.Instance)
	}
	if web.Usage.Usage.CPUMilli != 30 {
		t.Fatalf("web metered at %dm, want its own 30m", web.Usage.Usage.CPUMilli)
	}
	if web.Usage.Requests.CPUMilli != 100 {
		t.Fatalf("web reserved %dm, want 100m", web.Usage.Requests.CPUMilli)
	}
}
