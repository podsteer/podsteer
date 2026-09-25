package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// manifest decodes YAML-ish JSON into the shape a decoded manifest has.
//
// WRITTEN AS JSON RATHER THAN AS A TYPED OBJECT so the test asserts against
// the same map[string]any the API server's own response decodes into — which
// is what the rules read. A typed fixture would silently supply defaults and
// drop unknown fields, and both of those are exactly the conditions these
// rules have to survive on a CRD nobody here has a Go type for.
func manifest(t *testing.T, raw string) map[string]any {
	t.Helper()

	var object map[string]any
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	return object
}

// reference is one expected reference, flattened for comparison.
type reference struct {
	group     string
	version   string
	kind      string
	name      string
	namespace string
	via       string
}

func flatten(refs []domain.ObjectReference) []reference {
	out := make([]reference, 0, len(refs))
	for _, ref := range refs {
		out = append(out, reference{
			group: ref.Group, version: ref.Version, kind: ref.Kind,
			name: ref.Name, namespace: ref.Namespace, via: ref.Via,
		})
	}
	return out
}

func TestReferencesFromManifest(t *testing.T) {
	tests := []struct {
		name string
		kind string
		raw  string
		want []reference
	}{
		{
			name: "an ingress names its class, its backends and its certificates",
			kind: "Ingress",
			raw: `{
			  "metadata": {"name": "shop", "namespace": "shop"},
			  "spec": {
			    "ingressClassName": "nginx",
			    "tls": [{"secretName": "shop-tls"}],
			    "rules": [{"host": "shop.example.com", "http": {"paths": [
			      {"path": "/", "backend": {"service": {"name": "web"}}},
			      {"path": "/api", "backend": {"service": {"name": "api"}}}
			    ]}}]
			  }
			}`,
			want: []reference{
				{group: "networking.k8s.io", version: "v1", kind: "IngressClass", name: "nginx", via: "ingress class"},
				{version: "v1", kind: "Service", name: "web", namespace: "shop", via: "/"},
				{version: "v1", kind: "Service", name: "api", namespace: "shop", via: "/api"},
				{version: "v1", kind: "Secret", name: "shop-tls", namespace: "shop", via: "tls certificate"},
			},
		},
		{
			// THE DEFAULT BACKEND COUNTS: an Ingress with no rules still routes
			// everything to it, and an Ingress that drew nothing would read as
			// routing nowhere.
			name: "an ingress with only a default backend still routes somewhere",
			kind: "Ingress",
			raw: `{
			  "metadata": {"name": "shop", "namespace": "shop"},
			  "spec": {"defaultBackend": {"service": {"name": "web"}}}
			}`,
			want: []reference{
				{version: "v1", kind: "Service", name: "web", namespace: "shop", via: "default backend"},
			},
		},
		{
			// A resource backend names a kind in an arbitrary API group, and
			// the version is the cluster's business — hence a group with no
			// version rather than a guessed one.
			name: "an ingress resource backend names its own kind",
			kind: "Ingress",
			raw: `{
			  "metadata": {"name": "shop", "namespace": "shop"},
			  "spec": {"rules": [{"http": {"paths": [
			    {"path": "/assets", "backend": {"resource": {
			      "apiGroup": "k8s.example.com", "kind": "StorageBucket", "name": "assets"}}}
			  ]}}]}
			}`,
			want: []reference{
				{group: "k8s.example.com", kind: "StorageBucket", name: "assets", namespace: "shop", via: "/assets"},
			},
		},
		{
			name: "an ingress with nothing in it names nothing",
			kind: "Ingress",
			raw:  `{"metadata": {"name": "shop", "namespace": "shop"}, "spec": {}}`,
			want: []reference{},
		},
		{
			name: "a claim names its volume and its class",
			kind: "PersistentVolumeClaim",
			raw: `{
			  "metadata": {"name": "data", "namespace": "shop"},
			  "spec": {"volumeName": "pv-7", "storageClassName": "fast"}
			}`,
			want: []reference{
				{version: "v1", kind: "PersistentVolume", name: "pv-7", via: "bound to"},
				{group: "storage.k8s.io", version: "v1", kind: "StorageClass", name: "fast", via: "storage class"},
			},
		},
		{
			// AN UNBOUND CLAIM NAMES NO VOLUME. spec.volumeName is written by
			// the binder, so its absence means "still pending" — drawing a
			// missing box for it would report a broken reference where there
			// is simply not one yet.
			name: "an unbound claim names no volume",
			kind: "PersistentVolumeClaim",
			raw: `{
			  "metadata": {"name": "data", "namespace": "shop"},
			  "spec": {"storageClassName": "fast"}
			}`,
			want: []reference{
				{group: "storage.k8s.io", version: "v1", kind: "StorageClass", name: "fast", via: "storage class"},
			},
		},
		{
			// An EMPTY storageClassName explicitly opts out of dynamic
			// provisioning; it does not name a class called "".
			name: "an empty storage class names nothing",
			kind: "PersistentVolumeClaim",
			raw: `{
			  "metadata": {"name": "data", "namespace": "shop"},
			  "spec": {"storageClassName": ""}
			}`,
			want: []reference{},
		},
		{
			name: "a claim names what it was cloned from",
			kind: "PersistentVolumeClaim",
			raw: `{
			  "metadata": {"name": "data", "namespace": "shop"},
			  "spec": {"dataSourceRef": {
			    "apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly"}}
			}`,
			want: []reference{
				{group: "snapshot.storage.k8s.io", kind: "VolumeSnapshot", name: "nightly",
					namespace: "shop", via: "cloned from"},
			},
		},
		{
			// THE ONE GENUINELY CROSS-NAMESPACE REFERENCE the map meets: a
			// PersistentVolume is cluster-scoped and its claimRef names a
			// namespace of its own, so the namespace cannot come from the
			// subject.
			name: "a volume's claim carries its own namespace",
			kind: "PersistentVolume",
			raw: `{
			  "metadata": {"name": "pv-7"},
			  "spec": {"claimRef": {"namespace": "shop", "name": "data"}, "storageClassName": "fast"}
			}`,
			want: []reference{
				{version: "v1", kind: "PersistentVolumeClaim", name: "data", namespace: "shop", via: "claimed by"},
				{group: "storage.k8s.io", version: "v1", kind: "StorageClass", name: "fast", via: "storage class"},
			},
		},
		{
			name: "an unclaimed volume names no claim",
			kind: "PersistentVolume",
			raw:  `{"metadata": {"name": "pv-7"}, "spec": {}}`,
			want: []reference{},
		},
		{
			name: "a service account names its tokens and pull secrets",
			kind: "ServiceAccount",
			raw: `{
			  "metadata": {"name": "runner", "namespace": "shop"},
			  "secrets": [{"name": "runner-token"}],
			  "imagePullSecrets": [{"name": "registry"}]
			}`,
			want: []reference{
				{version: "v1", kind: "Secret", name: "runner-token", namespace: "shop", via: "token"},
				{version: "v1", kind: "Secret", name: "registry", namespace: "shop", via: "pulls images"},
			},
		},
		{
			name: "an autoscaler names what it scales",
			kind: "HorizontalPodAutoscaler",
			raw: `{
			  "metadata": {"name": "web", "namespace": "shop"},
			  "spec": {"scaleTargetRef": {"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"}}
			}`,
			want: []reference{
				{group: "apps", version: "v1", kind: "Deployment", name: "web", namespace: "shop", via: "scales"},
			},
		},
		{
			name: "an autoscaler with no target names nothing",
			kind: "HorizontalPodAutoscaler",
			raw:  `{"metadata": {"name": "web", "namespace": "shop"}, "spec": {}}`,
			want: []reference{},
		},
		{
			// A ClusterRole is cluster-scoped whatever binds it, so the
			// binding's namespace must not be attached to it — a reference
			// carrying a namespace it does not have resolves against a path
			// that 404s.
			name: "a role binding names the role and its service accounts",
			kind: "RoleBinding",
			raw: `{
			  "metadata": {"name": "runner", "namespace": "shop"},
			  "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "view"},
			  "subjects": [
			    {"kind": "ServiceAccount", "name": "runner"},
			    {"kind": "User", "name": "alice@example.com"},
			    {"kind": "Group", "name": "team"}
			  ]
			}`,
			want: []reference{
				{group: "rbac.authorization.k8s.io", kind: "ClusterRole", name: "view", via: "grants"},
				{version: "v1", kind: "ServiceAccount", name: "runner", namespace: "shop", via: "granted to"},
			},
		},
		{
			// A Role is always in the binding's own namespace, unlike a
			// ClusterRole.
			name: "a namespaced role keeps the binding's namespace",
			kind: "RoleBinding",
			raw: `{
			  "metadata": {"name": "runner", "namespace": "shop"},
			  "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "editor"},
			  "subjects": []
			}`,
			want: []reference{
				{group: "rbac.authorization.k8s.io", kind: "Role", name: "editor",
					namespace: "shop", via: "grants"},
			},
		},
		{
			// A ClusterRoleBinding's subjects carry their own namespace, and
			// there is no binding namespace to fall back to.
			name: "a cluster role binding reads each subject's own namespace",
			kind: "ClusterRoleBinding",
			raw: `{
			  "metadata": {"name": "readers"},
			  "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "view"},
			  "subjects": [{"kind": "ServiceAccount", "name": "runner", "namespace": "shop"}]
			}`,
			want: []reference{
				{group: "rbac.authorization.k8s.io", kind: "ClusterRole", name: "view", via: "grants"},
				{version: "v1", kind: "ServiceAccount", name: "runner", namespace: "shop", via: "granted to"},
			},
		},
		{
			// NO RULE MEANS NO EDGES, deliberately. Guessing at a field that
			// looks like a reference — anything ending in "Ref" — would draw
			// lines out of a CRD's spec that the operator never meant as
			// references at all.
			name: "a custom resource names nothing without a rule",
			kind: "Widget",
			raw: `{
			  "metadata": {"name": "left", "namespace": "shop"},
			  "spec": {"targetRef": {"kind": "Thing", "name": "right"}}
			}`,
			want: []reference{},
		},
		{
			name: "a config map names nothing",
			kind: "ConfigMap",
			raw:  `{"metadata": {"name": "settings", "namespace": "shop"}, "data": {"a": "b"}}`,
			want: []reference{},
		},
		{
			name: "an object with no spec at all is not a panic",
			kind: "Ingress",
			raw:  `{"metadata": {"name": "shop", "namespace": "shop"}}`,
			want: []reference{},
		},
		{
			// A malformed manifest must not be read as a reference either. The
			// API server would never send this, but a CRD's spec is whatever
			// its author wrote.
			name: "fields of the wrong type name nothing",
			kind: "Ingress",
			raw: `{
			  "metadata": {"name": "shop", "namespace": "shop"},
			  "spec": {"ingressClassName": 7, "rules": "not-a-list", "tls": [42]}
			}`,
			want: []reference{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := flatten(domain.ReferencesFromManifest(test.kind, manifest(t, test.raw)))

			if len(got) != len(test.want) {
				t.Fatalf("got %d references %+v, want %d %+v", len(got), got, len(test.want), test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("reference %d = %+v, want %+v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestSelectorFromManifest(t *testing.T) {
	tests := []struct {
		name string
		kind string
		raw  string
		want map[string]string
	}{
		{
			name: "a service's selector is read",
			kind: "Service",
			raw:  `{"spec": {"selector": {"app": "web", "tier": "front"}}}`,
			want: map[string]string{"app": "web", "tier": "front"},
		},
		{
			// AN EMPTY SELECTOR IS NOT AN ABSENT ONE READ AS "everything": in
			// the Kubernetes API a Service with no selector has none at all —
			// an ExternalName, or Endpoints managed by hand.
			name: "a service with no selector selects nothing",
			kind: "Service",
			raw:  `{"spec": {"type": "ExternalName", "externalName": "shop.example.com"}}`,
			want: nil,
		},
		{
			name: "an empty selector map selects nothing",
			kind: "Service",
			raw:  `{"spec": {"selector": {}}}`,
			want: nil,
		},
		{
			// Widening a selector is the direction that draws edges which do
			// not exist, so a value that is not a string refuses the whole
			// selector rather than dropping the key.
			name: "a non-string selector value refuses the selector",
			kind: "Service",
			raw:  `{"spec": {"selector": {"app": "web", "replicas": 3}}}`,
			want: nil,
		},
		{
			// A WORKLOAD'S SELECTOR IS DELIBERATELY NOT READ HERE. It is a
			// LabelSelector with a matchExpressions form this flat map cannot
			// carry, and a selector that arrives lossy is worse than none —
			// the same reason attribution elsewhere goes by ownerReference.
			name: "a deployment's selector is not read",
			kind: "Deployment",
			raw:  `{"spec": {"selector": {"matchLabels": {"app": "web"}}}}`,
			want: nil,
		},
		{
			name: "a config map has no selector",
			kind: "ConfigMap",
			raw:  `{"data": {"a": "b"}}`,
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := domain.SelectorFromManifest(test.kind, manifest(t, test.raw))

			if len(got) != len(test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
			for key, value := range test.want {
				if got[key] != value {
					t.Errorf("selector[%q] = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}

func TestSplitAPIVersion(t *testing.T) {
	tests := []struct {
		apiVersion  string
		wantGroup   string
		wantVersion string
	}{
		// The core group is written as a bare version, which is the half
		// people get backwards.
		{apiVersion: "v1", wantGroup: "", wantVersion: "v1"},
		{apiVersion: "apps/v1", wantGroup: "apps", wantVersion: "v1"},
		{apiVersion: "networking.k8s.io/v1", wantGroup: "networking.k8s.io", wantVersion: "v1"},
		{apiVersion: "", wantGroup: "", wantVersion: ""},
	}

	for _, test := range tests {
		t.Run(test.apiVersion, func(t *testing.T) {
			group, version := domain.SplitAPIVersion(test.apiVersion)
			if group != test.wantGroup || version != test.wantVersion {
				t.Errorf("SplitAPIVersion(%q) = %q, %q, want %q, %q",
					test.apiVersion, group, version, test.wantGroup, test.wantVersion)
			}
		})
	}
}
