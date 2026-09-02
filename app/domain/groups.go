package domain

import "strings"

// Naming the projects that own API groups.
//
// A CRD's API group is the only grouping the API server gives us, and it is
// the right MECHANISM — it is what the objects themselves say, it needs no
// heuristic, and it costs nothing. It is not a good LABEL: nobody reading a
// navigator wants a heading that says `monitoring.coreos.com`.
//
// SO THE TABLE NAMES THE PROJECT THAT OWNS THE GROUP, AND NOTHING FINER.
// `argoproj.io` is Argo CD, Argo Workflows, Argo Rollouts and Argo Events —
// four products behind one group — so the heading is "Argo" and never "Argo
// CD". Naming a product from a group is asserting that a particular
// controller is installed, and being wrong about that is worse than being
// vague: a heading claiming Argo CD on a cluster that runs only Argo
// Workflows teaches an operator to distrust every other heading here. It is
// the same reasoning gitops.ts already records about naming a GitOps owner.
//
// An uncurated group is shown VERBATIM. No stripping of suffixes, no
// title-casing of the first label: `example.crossplane.io` prettified into
// "Example" is a name nobody chose and nothing can be searched for, while the
// raw group is at least exactly what `kubectl api-resources` prints.

// groupOwners maps an API group to the project that publishes it.
//
// Extended by adding a line. Deliberately short: every entry is a claim about
// somebody else's software, and an entry that is merely plausible is worse
// than no entry at all.
var groupOwners = map[string]string{
	"argoproj.io":                    "Argo",
	"cert-manager.io":                "cert-manager",
	"acme.cert-manager.io":           "cert-manager",
	"monitoring.coreos.com":          "Prometheus Operator",
	"external-secrets.io":            "External Secrets",
	"generators.external-secrets.io": "External Secrets",
	"crd.projectcalico.org":          "Calico",
	"projectcalico.org":              "Calico",
	"cilium.io":                      "Cilium",
	"istio.io":                       "Istio",
	"networking.istio.io":            "Istio",
	"security.istio.io":              "Istio",
	"telemetry.istio.io":             "Istio",
	"gateway.networking.k8s.io":      "Gateway API",
	"traefik.io":                     "Traefik",
	"traefik.containo.us":            "Traefik",
	"elbv2.k8s.aws":                  "AWS Load Balancer Controller",
	"karpenter.sh":                   "Karpenter",
	"karpenter.k8s.aws":              "Karpenter",
	"kustomize.toolkit.fluxcd.io":    "Flux",
	"source.toolkit.fluxcd.io":       "Flux",
	"helm.toolkit.fluxcd.io":         "Flux",
	"notification.toolkit.fluxcd.io": "Flux",
	"image.toolkit.fluxcd.io":        "Flux",
	"keda.sh":                        "KEDA",
	"eventing.keda.sh":               "KEDA",
	"velero.io":                      "Velero",
	"postgresql.cnpg.io":             "CloudNativePG",
	"opentelemetry.io":               "OpenTelemetry",
	"jaegertracing.io":               "Jaeger",
	"kiali.io":                       "Kiali",
	"crossplane.io":                  "Crossplane",
	"pkg.crossplane.io":              "Crossplane",
	"apiextensions.crossplane.io":    "Crossplane",
	"tekton.dev":                     "Tekton",
	"triggers.tekton.dev":            "Tekton",
	"kyverno.io":                     "Kyverno",
	"gatekeeper.sh":                  "Gatekeeper",
	"sealedsecrets.bitnami.com":      "Sealed Secrets",
	"minio.min.io":                   "MinIO",
	"redis.redis.opstreelabs.in":     "Redis Operator",
	"kafka.strimzi.io":               "Strimzi",
	"core.strimzi.io":                "Strimzi",
	"clickhouse.altinity.com":        "Altinity ClickHouse",
	"mongodbcommunity.mongodb.com":   "MongoDB",
	"rabbitmq.com":                   "RabbitMQ",
	"longhorn.io":                    "Longhorn",
	"cluster.x-k8s.io":               "Cluster API",
	"metallb.io":                     "MetalLB",
	"kubevirt.io":                    "KubeVirt",
}

// GroupOwner names the project that publishes an API group.
//
// Falls back to the group itself, verbatim. An empty group is the core API
// group, which no custom resource uses.
func GroupOwner(group string) string {
	if group == "" {
		return "Core"
	}
	if owner, curated := groupOwners[group]; curated {
		return owner
	}

	// A subgroup of something curated: `stable.example.com` under
	// `example.com`. Following the parent is safe in a way inventing a name
	// is not — the parent's owner published the child.
	if _, parent, found := strings.Cut(group, "."); found {
		if owner, curated := groupOwners[parent]; curated {
			return owner
		}
	}

	return group
}
