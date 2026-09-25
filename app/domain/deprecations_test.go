package domain_test

// Tests for the upgrade-impact table.
//
// Weighted, like release_test.go, towards what happens with input the table
// does not recognise — a served group/version it has no entry for, a target
// it cannot place — because that is where a hand-compiled table can do real
// harm if it is wrong: telling somebody an upgrade breaks something it does
// not, or staying silent about one that actually does.

import (
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// findingByID returns the finding with the given ID, since UpgradeImpact's
// titles carry the target version and are not fixed strings worth matching
// on the way findingByTitle (overview_test.go) does for the rest of the
// package.
func findingByID(findings []domain.Finding, id string) (domain.Finding, bool) {
	for _, finding := range findings {
		if finding.ID == id {
			return finding, true
		}
	}
	return domain.Finding{}, false
}

// pdbV1Beta1 is the table entry used throughout: policy/v1beta1
// PodDisruptionBudget, removed in 1.25, replaced by policy/v1 — one of the
// entries the task itself names, and confirmed on the migration guide.
var pdbV1Beta1 = domain.Deprecation{
	Group: "policy", Version: "v1beta1", Kind: "PodDisruptionBudget", Resource: "poddisruptionbudgets",
	DeprecatedIn: "1.21", RemovedIn: "1.25", ReplacedBy: "policy/v1",
}

var pdbServed = []domain.APIGroupVersion{{Group: "policy", Version: "v1beta1"}}

func TestUpgradeImpactCriticalWhenWritersExist(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {
			Writers: []domain.APIWriter{
				{Manager: "helm", Namespace: "payments", Name: "checkout-budget"},
			},
			Scanned: 3,
		},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	wantID := "upgrade:" + pdbV1Beta1.ResourceKind().ID()
	finding, ok := findingByID(findings, wantID)
	if !ok {
		t.Fatalf("findings = %v, want one for %s", findings, wantID)
	}
	if finding.Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical", finding.Severity)
	}
	if finding.Count != 1 {
		t.Errorf("count = %d, want 1", finding.Count)
	}
	if finding.KindID != "" {
		t.Errorf("kindID = %q, want empty: the navigator has no entry for a removed version", finding.KindID)
	}
	if len(finding.Subjects) != 1 {
		t.Fatalf("subjects = %v, want exactly one writer", finding.Subjects)
	}
	subject := finding.Subjects[0]
	if subject.Namespace != "payments" || subject.Name != "checkout-budget" {
		t.Errorf("subject = %+v, want the writer's namespace and name", subject)
	}
	if !strings.Contains(subject.Detail, "helm") {
		t.Errorf("subject detail = %q, want it to name the manager", subject.Detail)
	}
	// Advice names the replacement and the manager, per the requirement that
	// a finding never leaves an operator to go find either themselves.
	if want := "policy/v1"; !strings.Contains(finding.Advice, want) {
		t.Errorf("advice = %q, want it to name %q", finding.Advice, want)
	}
	if want := "helm"; !strings.Contains(finding.Advice, want) {
		t.Errorf("advice = %q, want it to name the writer", finding.Advice)
	}
}

// A manager writing the same object twice — an Update and later an Apply,
// say — must be counted once, and the advice must name it once.
func TestUpgradeImpactDedupesWriters(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {
			Writers: []domain.APIWriter{
				{Manager: "helm", Namespace: "payments", Name: "checkout-budget"},
				{Manager: "helm", Namespace: "payments", Name: "checkout-budget"},
			},
			Scanned: 1,
		},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)
	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want a finding", findings)
	}
	if want := "helm, helm"; strings.Contains(finding.Advice, want) {
		t.Errorf("advice = %q, want the manager named once, not %q", finding.Advice, want)
	}
}

// Truncation is a fact worth stating in its own right — the number checked
// is not the whole story when the scan gave up early.
func TestUpgradeImpactTruncatedSummaryNamesObjectsChecked(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {
			Writers:   []domain.APIWriter{{Manager: "helm", Name: "x"}},
			Scanned:   2000,
			Truncated: true,
		},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)
	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want a finding", findings)
	}
	if want := "First 2000 objects checked."; !strings.Contains(finding.Summary, want) {
		t.Errorf("summary = %q, want it to mention %q", finding.Summary, want)
	}
}

func TestUpgradeImpactWarningWhenCheckedButNoWriters(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {Scanned: 4},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want one for %s", findings, pdbV1Beta1.ResourceKind().ID())
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning", finding.Severity)
	}
	if finding.Count != 0 {
		t.Errorf("count = %d, want 0", finding.Count)
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Detail != "no writers found" {
		t.Errorf("subjects = %v, want a single 'no writers found' subject", finding.Subjects)
	}
}

func TestUpgradeImpactWarningWhenUsageNotChecked(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}

	findings := domain.UpgradeImpact(pdbServed, current, target, nil)

	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want one for %s", findings, pdbV1Beta1.ResourceKind().ID())
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning", finding.Severity)
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Detail != "usage not checked" {
		t.Errorf("subjects = %v, want a single 'usage not checked' subject", finding.Subjects)
	}
	if want := "could not list"; !strings.Contains(finding.Advice, want) {
		t.Errorf("advice = %q, want it to say the list could not be checked", finding.Advice)
	}
}

func TestUpgradeImpactNothingWhenGroupVersionNotServed(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	// The GA version, not the deprecated one — this cluster already
	// migrated, and the table has no entry for policy/v1.
	served := []domain.APIGroupVersion{{Group: "policy", Version: "v1"}}

	findings := domain.UpgradeImpact(served, current, target, nil)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for a group/version already on its replacement", findings)
	}
}

func TestUpgradeImpactNothingForUnknownTarget(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "not-a-version"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {Writers: []domain.APIWriter{{Manager: "helm"}}},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for an unparseable target", findings)
	}
}

func TestUpgradeImpactNothingForUnknownCurrent(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: ""}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {Writers: []domain.APIWriter{{Manager: "helm"}}},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for an unparseable current version", findings)
	}
}

func TestUpgradeImpactNothingForATargetBehindCurrent(t *testing.T) {
	t.Parallel()

	// Not an upgrade. Comparing against a version behind where the cluster
	// already is would describe moving backwards as removing something.
	current := domain.ServerVersion{GitVersion: "v1.26.0"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {Writers: []domain.APIWriter{{Manager: "helm"}}},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for a target behind current", findings)
	}
}

// A CRD reusing the same version as a real removal, in a group Kubernetes
// does not own, must never match: the group is part of the key, not a hint.
func TestUpgradeImpactNeverFlagsACustomResourceGroup(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	served := []domain.APIGroupVersion{{Group: "example.com", Version: "v1beta1"}}

	findings := domain.UpgradeImpact(served, current, target, nil)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for a custom resource group", findings)
	}
}

// Already gone as of the CURRENT version is not something an upgrade causes,
// and must not be reported as though upgrading were the reason.
func TestUpgradeImpactNothingWhenAlreadyRemovedAtCurrent(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.26.0"} // past PDB v1beta1's removal at 1.25
	target := domain.ServerVersion{GitVersion: "v1.27.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {Writers: []domain.APIWriter{{Manager: "helm"}}},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	if len(findings) != 0 {
		t.Errorf("findings = %v, want none once the removal is already behind current", findings)
	}
}

// A deprecated API that survives the chosen target is worth an info-level
// note, not silence and not a warning — nothing breaks at this upgrade.
func TestUpgradeImpactInfoWhenDeprecatedButSurvivesTarget(t *testing.T) {
	t.Parallel()

	// flowcontrol.apiserver.k8s.io/v1beta2: deprecated since 1.26, removed
	// at 1.29. A cluster on 1.27 checking against 1.28 has already crossed
	// the deprecation line but not the removal one.
	served := []domain.APIGroupVersion{{Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta2"}}
	current := domain.ServerVersion{GitVersion: "v1.27.0"}
	target := domain.ServerVersion{GitVersion: "v1.28.0"}
	usage := map[string]domain.APIUsage{
		"flowcontrol.apiserver.k8s.io/v1beta2/flowschemas": {
			Writers: []domain.APIWriter{{Manager: "cluster-autoscaler"}},
		},
	}

	findings := domain.UpgradeImpact(served, current, target, usage)

	wantID := "upgrade:flowcontrol.apiserver.k8s.io/v1beta2/flowschemas"
	finding, ok := findingByID(findings, wantID)
	if !ok {
		t.Fatalf("findings = %v, want an info finding for %s", findings, wantID)
	}
	if finding.Severity != domain.SeverityInfo {
		t.Errorf("severity = %q, want info", finding.Severity)
	}
	if want := "flowcontrol.apiserver.k8s.io/v1"; !strings.Contains(finding.Advice, want) {
		t.Errorf("advice = %q, want it to name %q", finding.Advice, want)
	}
	// Empty, exactly as removalFinding's own KindID is: the navigator has no
	// entry for a deprecated version, whether it is still served or already
	// removed, so a dead click-through is worse than none.
	if finding.KindID != "" {
		t.Errorf("kindID = %q, want empty: the navigator has no entry for a deprecated version", finding.KindID)
	}
}

// A table entry the guide never gave a deprecation date for
// (PodSecurityPolicy) must not be reported as "still served past target" —
// there is no date to compare target against, and guessing one is exactly
// what this table exists not to do. It is still reported once the target
// reaches its removal.
func TestUpgradeImpactBlankDeprecatedInNeverProducesAnInfoFinding(t *testing.T) {
	t.Parallel()

	served := []domain.APIGroupVersion{{Group: "policy", Version: "v1beta1"}}
	current := domain.ServerVersion{GitVersion: "v1.22.0"}
	target := domain.ServerVersion{GitVersion: "v1.23.0"} // before PSP's 1.25 removal

	// v1beta1 policy also matches PodDisruptionBudget, which HAS a
	// DeprecatedIn — so this checks PodSecurityPolicy's own entry
	// specifically, by ID.
	findings := domain.UpgradeImpact(served, current, target, nil)
	pspID := "upgrade:policy/v1beta1/podsecuritypolicies"
	if _, ok := findingByID(findings, pspID); ok {
		t.Errorf("findings = %v, want none for %s: no confirmed deprecation date to report against", findings, pspID)
	}

	// Once the target reaches the confirmed removal, it is reported as any
	// other removal would be.
	pastRemoval := domain.ServerVersion{GitVersion: "v1.25.0"}
	findings = domain.UpgradeImpact(served, current, pastRemoval, nil)
	finding, ok := findingByID(findings, pspID)
	if !ok {
		t.Fatalf("findings = %v, want a removal finding once target reaches 1.25", findings)
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning: usage was not checked", finding.Severity)
	}
}

// A cluster that offers only entries the table has no removal-independent
// order for still gets UpgradeCandidates back in TABLE order, not served
// order — a stable order matters for the caller fanning out one scan per
// candidate.
func TestUpgradeCandidatesOrderAndMatching(t *testing.T) {
	t.Parallel()

	served := []domain.APIGroupVersion{
		{Group: "flowcontrol.apiserver.k8s.io", Version: "v1beta3"},
		{Group: "policy", Version: "v1beta1"},
		// Never in the table: must not appear in the result.
		{Group: "example.com", Version: "v1"},
	}

	candidates := domain.UpgradeCandidates(served)

	if len(candidates) < 3 {
		t.Fatalf("candidates = %v, want at least the flowcontrol and policy entries", candidates)
	}
	for _, c := range candidates {
		if c.Group == "example.com" {
			t.Errorf("candidates = %v, want no entry for an unserved custom group", candidates)
		}
	}
	// The table lists policy/v1beta1's entries (the 1.25 section) before
	// flowcontrol.apiserver.k8s.io/v1beta3's (the 1.32 section), so a served
	// set containing both keeps that order rather than the served slice's.
	firstGroup := candidates[0].Group
	if firstGroup != "policy" {
		t.Errorf("first candidate group = %q, want table order preserved", firstGroup)
	}
}

func TestAPIGroupVersionString(t *testing.T) {
	t.Parallel()

	if got := (domain.APIGroupVersion{Version: "v1"}).String(); got != "v1" {
		t.Errorf("core group String() = %q, want %q", got, "v1")
	}
	if got := (domain.APIGroupVersion{Group: "apps", Version: "v1"}).String(); got != "apps/v1" {
		t.Errorf("grouped String() = %q, want %q", got, "apps/v1")
	}
}

// A cluster whose discovery could not be read must not be assessed as though
// it served nothing: APIsKnown false means "unknown", and Upgrade must stay
// zero and produce no Upgrade-category findings, whatever else is true.
func TestNewOverviewWithAPIsUnknownProducesNoUpgradeAssessment(t *testing.T) {
	t.Parallel()

	overview := domain.NewOverview(domain.OverviewInput{
		Version:   domain.ServerVersion{GitVersion: "v1.24.9"},
		APIsKnown: false,
		ServedAPIs: []domain.APIGroupVersion{
			{Group: "policy", Version: "v1beta1"},
		},
		APIUsage: map[string]domain.APIUsage{
			pdbV1Beta1.ResourceKind().ID(): {Writers: []domain.APIWriter{{Manager: "helm"}}},
		},
	})

	if overview.Upgrade.TargetMinor != "" {
		t.Errorf("upgrade target = %q, want empty: discovery could not be read", overview.Upgrade.TargetMinor)
	}
	if overview.Upgrade.Count != 0 {
		t.Errorf("upgrade count = %d, want 0", overview.Upgrade.Count)
	}
	for _, finding := range overview.Findings {
		if finding.Category == domain.CategoryFindingUpgrade {
			t.Errorf("findings = %v, want no Upgrade-category finding when APIs are unknown", overview.Findings)
		}
	}
}

// A resource whose only writers are the control plane maintaining its own
// default objects must not read as critical — the old producer's
// managedFields entry surviving an upgrade on an object nobody actually
// wrote is not evidence anything breaks.
func TestUpgradeImpactIgnoresSelfManagedWriters(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {
			Writers: []domain.APIWriter{
				{Manager: "api-priority-and-fairness-config-producer-v1", Name: "exempt", SelfManaged: true},
			},
			Scanned: 1,
		},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want one for %s", findings, pdbV1Beta1.ResourceKind().ID())
	}
	if finding.Severity != domain.SeverityWarning {
		t.Errorf("severity = %q, want warning: the only writer is self-managed", finding.Severity)
	}
	if finding.Count != 0 {
		t.Errorf("count = %d, want 0: a self-managed writer does not count", finding.Count)
	}
	if want := "1 object maintained by the control plane itself"; !strings.Contains(finding.Summary, want) {
		t.Errorf("summary = %q, want it to mention %q", finding.Summary, want)
	}
}

// A mix of a real writer and a self-managed one must still be critical, but
// the count, subjects and advice must cover only the operator's own writer —
// the self-managed one is named separately, not folded into the total.
func TestUpgradeImpactCountsOnlyOperatorWriters(t *testing.T) {
	t.Parallel()

	current := domain.ServerVersion{GitVersion: "v1.24.9"}
	target := domain.ServerVersion{GitVersion: "v1.25.0"}
	usage := map[string]domain.APIUsage{
		pdbV1Beta1.ResourceKind().ID(): {
			Writers: []domain.APIWriter{
				{Manager: "helm", Namespace: "payments", Name: "checkout-budget"},
				{Manager: "api-priority-and-fairness-config-producer-v1", Name: "exempt", SelfManaged: true},
			},
			Scanned: 2,
		},
	}

	findings := domain.UpgradeImpact(pdbServed, current, target, usage)

	finding, ok := findingByID(findings, "upgrade:"+pdbV1Beta1.ResourceKind().ID())
	if !ok {
		t.Fatalf("findings = %v, want one for %s", findings, pdbV1Beta1.ResourceKind().ID())
	}
	if finding.Severity != domain.SeverityCritical {
		t.Errorf("severity = %q, want critical: an operator writer is still present", finding.Severity)
	}
	if finding.Count != 1 {
		t.Errorf("count = %d, want 1: only the operator writer counts", finding.Count)
	}
	if len(finding.Subjects) != 1 || finding.Subjects[0].Name != "checkout-budget" {
		t.Errorf("subjects = %v, want only the operator's writer", finding.Subjects)
	}
	if want := "1 object maintained by the control plane itself"; !strings.Contains(finding.Summary, want) {
		t.Errorf("summary = %q, want it to name how many self-managed writers were skipped", finding.Summary)
	}
}
