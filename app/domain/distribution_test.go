package domain

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The version string is the control plane saying what it is, and it beats
// everything else — including a providerID that names somewhere else entirely.
func TestTheVersionStringWins(t *testing.T) {
	t.Parallel()

	got := IdentifyDistribution(DistributionInput{
		GitVersion: "v1.30.2+k3s1",
		// Running on somebody's cloud VMs, which is the ordinary case for a
		// self-hosted cluster and the one that must not be mislabelled.
		ProviderID: "aws:///eu-west-1a/i-0123456789",
		Host:       "10.0.0.1",
	})

	if got.Label != "k3s" {
		t.Fatalf("label = %q, want k3s: a self-hosted cluster on cloud VMs is not a managed one", got.Label)
	}
	if got.Hosted {
		t.Error("k3s reported as a hosted control plane")
	}
}

// THE CASE THIS RANKING EXISTS FOR. Calling a self-hosted cluster by its
// provider's name sends an operator to a console to look for a cluster that
// was never there.
func TestInfrastructureIsOnlyTheAnswerWhenNothingElseIs(t *testing.T) {
	t.Parallel()

	unknown := IdentifyDistribution(DistributionInput{ProviderID: "hcloud://12345"})
	if unknown.IsZero() {
		t.Fatal("a node reporting its provider produced no answer at all")
	}
	if unknown.Hosted {
		t.Error("an infrastructure answer claimed a hosted control plane")
	}
	if !strings.Contains(unknown.Evidence, "run on") {
		t.Errorf("evidence = %q, want it to say this is where the nodes run", unknown.Evidence)
	}
}

// A context nobody has opened has only its address, and that is enough for the
// managed ones whose API servers live on a recognisable domain.
func TestAHostAloneIdentifiesTheManagedOnes(t *testing.T) {
	t.Parallel()

	for host, want := range map[string]bool{
		"example.eks.amazonaws.com":         true,
		"example.azmk8s.io":                 true,
		"example.k8s.ondigitalocean.com":    true,
		"kubernetes.internal.example.co.uk": false,
		"10.0.0.1":                          false,
	} {
		got := IdentifyDistribution(DistributionInput{Host: host})
		if got.IsZero() == want {
			t.Errorf("%s: identified = %v, want %v", host, !got.IsZero(), want)
		}
	}
}

// NOTHING IS GUESSED. A cluster that matches no rule carries no mark, which is
// a truthful blank rather than a hedge — and the alternative, a nearest guess,
// is how somebody comes to trust a label that was never evidence.
func TestAnUnrecognisedClusterIsLeftAlone(t *testing.T) {
	t.Parallel()

	got := IdentifyDistribution(DistributionInput{
		Host:       "kubernetes.example.internal",
		GitVersion: "v1.31.0",
		NodeLabels: map[string]string{"kubernetes.io/os": "linux"},
	})

	if !got.IsZero() {
		t.Errorf("distribution = %+v, want nothing", got)
	}
}

// A node label identifies the managed control planes that do NOT decorate
// their version string, which is why the ladder has more than one rung.
func TestANodeLabelIdentifiesWhatTheVersionDoesNot(t *testing.T) {
	t.Parallel()

	got := IdentifyDistribution(DistributionInput{
		GitVersion: "v1.31.0",
		NodeLabels: map[string]string{"kubernetes.azure.com/cluster": "MC_rg_cluster_westeurope"},
	})

	if got.IsZero() || !got.Hosted {
		t.Fatalf("distribution = %+v, want a hosted one", got)
	}
}

// More evidence can only improve the answer: the ranking is fixed rather than
// last-write-wins, so refining on connect cannot make a mark worse.
func TestMoreEvidenceNeverMakesTheAnswerWorse(t *testing.T) {
	t.Parallel()

	host := "example.eks.amazonaws.com"
	fromHost := IdentifyDistribution(DistributionInput{Host: host})
	refined := IdentifyDistribution(DistributionInput{
		Host:       host,
		GitVersion: "v1.32.7-eks-1234567",
		NodeLabels: map[string]string{"eks.amazonaws.com/nodegroup": "workers"},
		ProviderID: "aws:///eu-west-1a/i-0123456789",
	})

	if fromHost.ID != refined.ID {
		t.Errorf("host said %q and the cluster said %q; refining changed the answer", fromHost.ID, refined.ID)
	}
	if refined.Evidence == fromHost.Evidence {
		t.Error("refining did not improve the evidence, so the tooltip still cites the weakest signal")
	}
}

// A mark stored on a previous run resolves to its label, and one whose row has
// since been removed is forgotten rather than rendered as its own id.
func TestAStoredMarkResolvesOrIsForgotten(t *testing.T) {
	t.Parallel()

	known := IdentifyDistribution(DistributionInput{GitVersion: "v1.30.2+k3s1"})
	back, ok := DistributionByID(known.ID)
	if !ok || back.Label != known.Label {
		t.Errorf("DistributionByID(%q) = %+v, %v", known.ID, back, ok)
	}

	if _, ok := DistributionByID("a-row-that-was-removed"); ok {
		t.Error("an unknown id resolved; a mark nothing can explain must be forgotten")
	}
}

// The same rule vendorclis.json holds: the table is data, so no distribution
// or provider is named in Go code. Comments are exempt for the reason stated
// in TestNoProviderIsNamedInGoSource.
func TestNoDistributionIsNamedInGoCode(t *testing.T) {
	t.Parallel()

	distributionOnce.Do(loadDistributions)
	if distributionErr != nil {
		t.Fatalf("loading the table: %v", distributionErr)
	}

	var labels []string
	for _, rule := range distributionTable_.Distributions {
		labels = append(labels, rule.Label)
	}
	for _, rule := range distributionTable_.Infrastructure {
		labels = append(labels, rule.Label)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		code := withoutComments(string(body))

		for _, label := range labels {
			if ambiguousLabels[strings.ToLower(label)] {
				continue
			}
			word := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(label) + `\b`)
			if word.MatchString(code) {
				t.Errorf("%s names %q; every distribution-specific fact belongs in distributions.json", name, label)
			}
		}
	}
}

// ambiguousLabels are marks whose names are also ordinary vocabulary here, so
// this guard cannot say anything about them.
//
// SAID RATHER THAN QUIETLY SKIPPED. "kind" is a local-cluster tool AND the
// field on every Kubernetes object there is, and no amount of word-boundary
// matching separates the two — a test that failed on `object.Kind` would be
// deleted within the week. The rule it enforces still applies to those rows;
// what is missing is a mechanical check, and pretending otherwise would be
// worse than the gap.
var ambiguousLabels = map[string]bool{"kind": true}
