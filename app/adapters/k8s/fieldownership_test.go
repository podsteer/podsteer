package k8s

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// ownershipManifest is the shape a real object carries: one GitOps controller
// owning spec through a keyed container, the control plane owning status
// through a subresource, and a `.` marker for a map owned whole.
const ownershipManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: default
  managedFields:
  - manager: argocd-controller
    operation: Apply
    apiVersion: apps/v1
    time: "2026-09-11T15:17:59Z"
    fieldsType: FieldsV1
    fieldsV1:
      f:metadata:
        f:labels:
          .: {}
          f:app: {}
      f:spec:
        f:replicas: {}
        f:template:
          f:spec:
            f:containers:
              k:{"name":"web"}:
                .: {}
                f:image: {}
  - manager: kube-controller-manager
    operation: Update
    apiVersion: apps/v1
    subresource: status
    time: "2026-09-11T15:18:04Z"
    fieldsType: FieldsV1
    fieldsV1:
      f:status:
        f:readyReplicas: {}
spec:
  replicas: 3
`

// TestFieldOwnershipSpellsFieldsTheWayAConflictDoes is the reason this decodes
// through structured-merge-diff instead of walking the `f:`/`k:` keys.
//
// A conflict message from the API server names
// `.spec.template.spec.containers[name="web"].image` — verified against a live
// cluster, and recorded in apply_test.go. The ownership panel and the conflict
// dialog show the same field to the same operator, so they must spell it the
// same way, and generating both with the same library is the only version of
// that guarantee which cannot drift.
func TestFieldOwnershipSpellsFieldsTheWayAConflictDoes(t *testing.T) {
	ownership, err := decodeFieldOwnership(ownershipManifest)
	if err != nil {
		t.Fatalf("decodeFieldOwnership() error = %v", err)
	}
	if len(ownership) != 2 {
		t.Fatalf("got %d owners, want 2: %+v", len(ownership), ownership)
	}

	const keyed = `.spec.template.spec.containers[name="web"].image`
	if !hasField(ownership[0].Fields, keyed) {
		t.Errorf("fields = %v, want one spelled %q", ownership[0].Fields, keyed)
	}

	// The `.` marker is a fact of its own: this manager owns the labels map
	// whole AS WELL AS the one label in it. Decoding to leaves only would
	// drop the first, and with it the difference between "owns every label"
	// and "owns this label".
	for _, want := range []string{".metadata.labels", ".metadata.labels.app"} {
		if !hasField(ownership[0].Fields, want) {
			t.Errorf("fields = %v, want %q", ownership[0].Fields, want)
		}
	}

	// Sorted by construction — the walk is a preorder DFS over a sorted
	// structure — which is what lets the panel render them without sorting.
	if !sortedAscending(ownership[0].Fields) {
		t.Errorf("fields are not sorted: %v", ownership[0].Fields)
	}
}

// TestFieldOwnershipKeepsWhatDecidesWhatAnEntryMeans: a manager name alone
// does not say what the entry is.
func TestFieldOwnershipKeepsWhatDecidesWhatAnEntryMeans(t *testing.T) {
	ownership, err := decodeFieldOwnership(ownershipManifest)
	if err != nil {
		t.Fatalf("decodeFieldOwnership() error = %v", err)
	}

	argo := ownership[0]
	if argo.Kind != domain.ManagerGitOps {
		t.Errorf("Kind = %q, want %q", argo.Kind, domain.ManagerGitOps)
	}
	if argo.Operation != "Apply" {
		t.Errorf("Operation = %q, want Apply", argo.Operation)
	}
	if argo.UpdatedAt != "2026-09-11T15:17:59Z" {
		t.Errorf("UpdatedAt = %q", argo.UpdatedAt)
	}

	// The subresource is what says these fields are unreachable from a write
	// to the object itself. Dropping it would make the control plane look
	// like a competitor for fields nobody is competing for.
	status := ownership[1]
	if status.Subresource != "status" {
		t.Errorf("Subresource = %q, want status", status.Subresource)
	}
	if status.Kind != domain.ManagerControlPlane {
		t.Errorf("Kind = %q, want %q", status.Kind, domain.ManagerControlPlane)
	}
}

// TestFieldOwnershipKeepsBothOperationsOfOneManager is the split ApplyResource
// exists to resolve, seen from the other side.
//
// The server keys an entry on name AND operation, so podsteer/Update and
// podsteer/Apply are two rows holding different fields — and collapsing them
// here would hide the reason an operator was ever asked to take a field from
// PodSteer.
func TestFieldOwnershipKeepsBothOperationsOfOneManager(t *testing.T) {
	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: default
  managedFields:
  - manager: podsteer
    operation: Update
    apiVersion: v1
    fieldsType: FieldsV1
    fieldsV1:
      f:data:
        f:one: {}
  - manager: podsteer
    operation: Apply
    apiVersion: v1
    fieldsType: FieldsV1
    fieldsV1:
      f:data:
        f:two: {}
data:
  one: a
  two: b
`
	ownership, err := decodeFieldOwnership(manifest)
	if err != nil {
		t.Fatalf("decodeFieldOwnership() error = %v", err)
	}
	if len(ownership) != 2 {
		t.Fatalf("got %d owners, want both operations: %+v", len(ownership), ownership)
	}
	if ownership[0].Operation == ownership[1].Operation {
		t.Errorf("both entries say %q — the operations were collapsed", ownership[0].Operation)
	}
	if names := ownership.Managers(); len(names) != 1 || names[0] != "podsteer" {
		t.Errorf("Managers() = %v, want one name — it answers who wrote, not how", names)
	}
}

// TestFieldOwnershipOfAnObjectNobodyTrackedIsEmptyRatherThanAnError. An object
// created before server-side apply, or one the server keeps no record for,
// has nothing to show and is not a failure.
func TestFieldOwnershipOfAnObjectNobodyTrackedIsEmptyRatherThanAnError(t *testing.T) {
	ownership, err := decodeFieldOwnership("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\n")
	if err != nil {
		t.Fatalf("decodeFieldOwnership() error = %v", err)
	}
	if len(ownership) != 0 {
		t.Errorf("got %d owners, want none: %+v", len(ownership), ownership)
	}
}

// TestFieldOwnershipNeverRepeatsTheManifest is the Secrets rule applied to a
// verb that reads whole objects.
//
// A Secret's manifest reaches this function, and a parse failure is exactly
// where a library's error message tends to quote what it was given. The
// message may name the manager — that is not an object name and is already on
// screen — and must carry nothing else.
func TestFieldOwnershipNeverRepeatsTheManifest(t *testing.T) {
	const secret = `apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: default
  managedFields:
  - manager: kubectl-client-side-apply
    operation: Update
    apiVersion: v1
    fieldsType: FieldsV1
    fieldsV1: "not an object"
data:
  password: c3VwZXItc2VjcmV0
`
	_, err := decodeFieldOwnership(secret)
	if err == nil {
		t.Fatal("a fieldsV1 that is not an object was accepted")
	}
	for _, leaked := range []string{"c3VwZXItc2VjcmV0", "password", "db"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("error message carries %q: %v", leaked, err)
		}
	}
	if !strings.Contains(err.Error(), "kubectl-client-side-apply") {
		t.Errorf("error message does not say whose record failed to read: %v", err)
	}
}

func hasField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

func sortedAscending(fields []string) bool {
	for i := 1; i < len(fields); i++ {
		if fields[i-1] > fields[i] {
			return false
		}
	}
	return true
}
