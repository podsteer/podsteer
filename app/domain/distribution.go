// Recognising which Kubernetes a cluster is.
//
// WHY IT IS WORTH KNOWING. An operator with a dozen contexts in one kubeconfig
// is reading a list of near-identical names — the tab bar's own comment says
// as much — and "which of these is the managed one, and which is the k3s on a
// VM" is the distinction they most often want and the one a context name least
// often carries. A mark beside each row answers it without opening anything.
//
// TWO QUESTIONS, NOT ONE, and conflating them would produce a confident wrong
// answer. WHICH DISTRIBUTION is a fact about the control plane: EKS, k3s,
// OpenShift. WHICH INFRASTRUCTURE is a fact about where the nodes are: AWS,
// Hetzner, vSphere. A k3s on a cloud provider's VMs is k3s — reporting the
// provider there would tell somebody they have a managed cluster they cannot
// find in any console. So the distribution wins whenever it is known, and the
// infrastructure answers only when nothing identified the distribution.
//
// THE EVIDENCE IS RANKED, AND THE RANKING IS THE DESIGN:
//
//  1. The server's version string. A managed distribution decorates it —
//     "-gke.", "-eks-", "+k3s1" — and that is the control plane saying what it
//     is. Strongest, and free: it is read at connect already.
//  2. A node label only one distribution sets.
//  3. The node's providerID, which names infrastructure rather than
//     distribution, and is therefore the fallback rather than a peer.
//  4. The API server's HOST. Weakest, and the only one available BEFORE
//     connecting — which is exactly why it exists: the Home list shows every
//     context in the kubeconfig, most of them not open.
//
// Nothing is guessed. A cluster matching none of these carries no mark at all,
// which is a truthful blank rather than a hedge.
//
// AND THE TABLE IS DATA, for the reason vendorclis.json is: no distribution or
// provider is named in Go, so adding one is a diff to a file, and a test greps
// the package to keep it so.
package domain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed distributions.json
var distributionsRaw []byte

// Distribution is what a cluster turned out to be.
type Distribution struct {
	// ID is the stable key, and what is stored per context.
	ID string
	// Label is what an operator reads.
	Label string
	// Hosted reports a managed control plane — somebody else runs it. False
	// for the ones an operator runs themselves.
	Hosted bool
	// Evidence names what identified it, for the tooltip: an operator who
	// disagrees with a mark deserves to know why it is there.
	Evidence string
}

// IsZero reports that nothing identified this cluster.
func (d Distribution) IsZero() bool { return d.ID == "" }

// DistributionInput is what is known about a cluster, in whatever quantity.
//
// EVERY FIELD IS OPTIONAL, which is the point: the same function answers for a
// context nobody has opened — where only Host is known — and for one whose
// nodes have been read.
type DistributionInput struct {
	// Host is the API server's hostname, from the kubeconfig.
	Host string
	// GitVersion is the server's full version string, once it has answered.
	GitVersion string
	// NodeLabels are the labels of any one node.
	NodeLabels map[string]string
	// ProviderID is any one node's spec.providerID.
	ProviderID string
}

type distributionTable struct {
	Version        int                  `json:"version"`
	Distributions  []distributionRule   `json:"distributions"`
	Infrastructure []infrastructureRule `json:"infrastructure"`
}

type distributionRule struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Hosted           bool     `json:"hosted"`
	VersionFragments []string `json:"versionFragments"`
	NodeLabels       []string `json:"nodeLabels"`
	ProviderPrefixes []string `json:"providerPrefixes"`
	HostSuffixes     []string `json:"hostSuffixes"`
}

type infrastructureRule struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	ProviderPrefixes []string `json:"providerPrefixes"`
}

var (
	distributionOnce   sync.Once
	distributionTable_ distributionTable
	distributionErr    error
)

func loadDistributions() {
	if err := json.Unmarshal(distributionsRaw, &distributionTable_); err != nil {
		distributionErr = fmt.Errorf("reading the distribution table: %w", err)
		return
	}
	for _, rule := range distributionTable_.Distributions {
		if rule.ID == "" || rule.Label == "" {
			distributionErr = fmt.Errorf("a distribution row is missing an id or a label: %+v", rule)
			return
		}
	}
}

// IdentifyDistribution reports what a cluster is, from whatever is known.
//
// The ranking in this file's opening comment, in order, stopping at the first
// answer. A caller with only a host gets the host's answer; one with a version
// gets a better one; and because the ranking is fixed rather than
// last-write-wins, more evidence can only improve the answer.
func IdentifyDistribution(input DistributionInput) Distribution {
	distributionOnce.Do(loadDistributions)
	if distributionErr != nil {
		return Distribution{}
	}

	version := strings.ToLower(input.GitVersion)
	provider := strings.ToLower(input.ProviderID)
	host := strings.ToLower(strings.TrimSpace(input.Host))

	// 1. The control plane's own version string.
	if version != "" {
		for _, rule := range distributionTable_.Distributions {
			for _, fragment := range rule.VersionFragments {
				if fragment != "" && strings.Contains(version, strings.ToLower(fragment)) {
					return found(rule, "its version string says so")
				}
			}
		}
	}

	// 2. A label only one distribution sets.
	if len(input.NodeLabels) > 0 {
		for _, rule := range distributionTable_.Distributions {
			for _, key := range rule.NodeLabels {
				if _, ok := input.NodeLabels[key]; ok {
					return found(rule, "its nodes carry "+key)
				}
			}
		}
	}

	// 3. A providerID that a DISTRIBUTION claims — kind, and anything else
	//    whose nodes are unmistakable.
	if provider != "" {
		for _, rule := range distributionTable_.Distributions {
			for _, prefix := range rule.ProviderPrefixes {
				if prefix != "" && strings.HasPrefix(provider, strings.ToLower(prefix)) {
					return found(rule, "its nodes report "+prefix)
				}
			}
		}
	}

	// 4. The host, which is all a context nobody has opened can offer.
	if host != "" {
		for _, rule := range distributionTable_.Distributions {
			for _, suffix := range rule.HostSuffixes {
				if suffix != "" && strings.HasSuffix(host, strings.ToLower(suffix)) {
					return found(rule, "its API server's address")
				}
			}
		}
	}

	// 5. Nothing identified the distribution, so say where it RUNS instead.
	//    Deliberately last: a k3s on somebody's cloud VMs is k3s, and calling
	//    it by the provider's name would send an operator looking for a
	//    managed cluster that does not exist.
	if provider != "" {
		for _, rule := range distributionTable_.Infrastructure {
			for _, prefix := range rule.ProviderPrefixes {
				if prefix != "" && strings.HasPrefix(provider, strings.ToLower(prefix)) {
					return Distribution{
						ID:       rule.ID,
						Label:    rule.Label,
						Hosted:   false,
						Evidence: "its nodes run on " + rule.Label,
					}
				}
			}
		}
	}

	return Distribution{}
}

func found(rule distributionRule, evidence string) Distribution {
	return Distribution{ID: rule.ID, Label: rule.Label, Hosted: rule.Hosted, Evidence: evidence}
}

// Distributions returns every mark the table can produce.
//
// FOR THE FRONTEND TO RESOLVE A STORED ID, and to keep one source of truth for
// the labels: a table duplicated in TypeScript is a table that drifts, and the
// rule that no distribution is named in code applies on that side too.
func Distributions() []Distribution {
	distributionOnce.Do(loadDistributions)
	if distributionErr != nil {
		return nil
	}

	out := make([]Distribution, 0, len(distributionTable_.Distributions)+len(distributionTable_.Infrastructure))
	for _, rule := range distributionTable_.Distributions {
		out = append(out, Distribution{ID: rule.ID, Label: rule.Label, Hosted: rule.Hosted})
	}
	for _, rule := range distributionTable_.Infrastructure {
		out = append(out, Distribution{ID: rule.ID, Label: rule.Label})
	}
	return out
}

// DistributionByID returns a stored mark's label, for a context whose answer
// was worked out on a previous run.
//
// A STORED ID THAT NO LONGER EXISTS IS FORGOTTEN, not rendered as itself. A
// table row removed in an upgrade would otherwise leave a mark nothing can
// explain on a cluster nobody can re-identify.
func DistributionByID(id string) (Distribution, bool) {
	distributionOnce.Do(loadDistributions)
	if distributionErr != nil || id == "" {
		return Distribution{}, false
	}

	for _, rule := range distributionTable_.Distributions {
		if rule.ID == id {
			return found(rule, "identified when you last opened it"), true
		}
	}
	for _, rule := range distributionTable_.Infrastructure {
		if rule.ID == id {
			return Distribution{
				ID:       rule.ID,
				Label:    rule.Label,
				Evidence: "identified when you last opened it",
			}, true
		}
	}
	return Distribution{}, false
}
