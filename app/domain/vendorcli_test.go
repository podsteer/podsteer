package domain

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shipped table has to be runnable and readable before anything runs it:
// it is inside the binary, so a row that cannot work is a build-time mistake,
// and finding it when somebody presses a button would make it an operator's.
func TestTheShippedTableIsUsable(t *testing.T) {
	t.Parallel()

	clis, err := VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() error = %v", err)
	}
	if len(clis) == 0 {
		t.Fatal("the table is empty; the feature has nothing to offer")
	}

	for _, cli := range clis {
		if cli.Binary == "" || cli.Label == "" || cli.ID == "" {
			t.Errorf("row %+v is missing something the interface needs", cli)
		}
		if _, err := PlanVendorList(cli.ID); err != nil {
			t.Errorf("PlanVendorList(%q) = %v, want a plan", cli.ID, err)
		}
	}
}

// NO PROVIDER IS NAMED IN GO CODE. The whole reason the table is an embedded
// data file rather than a Go slice is that a provider can then be added,
// renamed or removed without touching code — and that only holds if nothing
// leaks back into the code. This is the mechanical check that keeps it true;
// without it the rule survives only as long as everyone remembers it.
//
// COMMENTS ARE EXEMPT, AND THAT IS NOT A LOOPHOLE. This package legitimately
// explains Kubernetes itself — that a managed distribution decorates its
// version string, that `--event-ttl` is not configurable on the hosted ones —
// and those sentences name distributions because the facts are about them. A
// test that failed on prose would be deleted rather than obeyed. What must
// not appear is a provider's BINARY or its LABEL in executable code, because
// that is what would make the code provider-specific.
func TestNoProviderIsNamedInGoSource(t *testing.T) {
	t.Parallel()

	clis, err := VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() error = %v", err)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		// This file names them, because it is the file that checks they are
		// not named anywhere else.
		if entry.Name() == "vendorcli_test.go" {
			continue
		}

		body, err := os.ReadFile(filepath.Join(".", entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		text := withoutComments(string(body))

		for _, cli := range clis {
			for _, needle := range []string{cli.Binary, cli.Label} {
				if needle == "" {
					continue
				}
				// WHOLE WORDS ONLY. "aks" is inside "tasks" and "eks" is
				// inside "weeks"; a substring match fails on prose that has
				// nothing to do with any provider, and a test that cries wolf
				// gets deleted rather than obeyed.
				word := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(needle) + `\b`)
				if word.MatchString(text) {
					t.Errorf("%s names %q; every provider-specific fact belongs in vendorclis.json", entry.Name(), needle)
				}
			}
		}
	}
}

// A row that asked for admin or embedded credentials would put a credential
// into the temporary kubeconfig this feature writes, which decision 12 says it
// does not do.
func TestNoRowAsksForAdminCredentials(t *testing.T) {
	t.Parallel()

	clis, err := VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() error = %v", err)
	}

	for _, cli := range clis {
		plan, err := PlanVendorAdd(cli.ID, VendorCluster{
			Name:   "example",
			Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
		}, "/tmp/kubeconfig")
		if err != nil {
			t.Fatalf("PlanVendorAdd(%q) = %v", cli.ID, err)
		}
		for _, arg := range plan.Args {
			if strings.Contains(strings.ToLower(arg), "admin") {
				t.Errorf("%s would ask for admin credentials: %v", cli.ID, plan.Args)
			}
		}
	}
}

// The plan carries its own bound, so no adapter invents one and no two
// timeouts can disagree about how long a run may take.
func TestThePlanCarriesItsTimeout(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	list, err := PlanVendorList(clis[0].ID)
	if err != nil {
		t.Fatalf("PlanVendorList() = %v", err)
	}
	if list.Timeout != VendorListTimeout {
		t.Errorf("list timeout = %v, want %v", list.Timeout, VendorListTimeout)
	}

	add, err := PlanVendorAdd(clis[0].ID, VendorCluster{
		Name:   "example",
		Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
	}, "/tmp/kubeconfig")
	if err != nil {
		t.Fatalf("PlanVendorAdd() = %v", err)
	}
	if add.Timeout != VendorAddTimeout {
		t.Errorf("add timeout = %v, want %v", add.Timeout, VendorAddTimeout)
	}
}

// EVERY ADD PLAN MUST KEEP THE CLI AWAY FROM THE OPERATOR'S KUBECONFIG. These
// CLIs set current-context when they write one, which would move the target of
// every kubectl in every other terminal on the machine. There are two ways to
// keep them off it: point them at a file PodSteer named, or use a subcommand
// that prints the document and writes nothing. A row that does neither would
// let the CLI write wherever it likes, which is the one thing this feature may
// not do.
func TestNoAddPlanLetsTheCLIChooseWhereToWrite(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	for _, cli := range clis {
		plan, err := PlanVendorAdd(cli.ID, VendorCluster{
			Name:   "example",
			Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
		}, "/tmp/podsteer-kubeconfig")
		if err != nil {
			t.Fatalf("PlanVendorAdd(%q) = %v", cli.ID, err)
		}

		switch {
		case plan.KubeconfigFlag != "":
			if !contains(plan.Args, "/tmp/podsteer-kubeconfig") {
				t.Errorf("%s: the path is not in the argv: %v", cli.ID, plan.Args)
			}
		case plan.KubeconfigEnv != "":
			// The adapter sets the variable; nothing to assert in the argv.
		case plan.KubeconfigOnStdout:
			// It prints and writes nothing, which is better than either: no
			// file exists to hold a credential even for a moment.
			if plan.KubeconfigFlag != "" || plan.KubeconfigEnv != "" {
				t.Errorf("%s: a printing row also points at a file", cli.ID)
			}
		default:
			t.Errorf("%s: the plan lets the CLI write wherever it likes", cli.ID)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAnUnknownProviderIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := PlanVendorList("nothing-like-this"); !errors.Is(err, ErrVendorUnknown) {
		t.Errorf("error = %v, want ErrVendorUnknown", err)
	}
}

// A value that would be read as a flag, or that carries whitespace or quoting,
// is refused however it arrived. Nothing here is passed to a shell — the
// adapter runs an argv — so this is not what stands between an operator and a
// command injection; it refuses two shapes that are wrong regardless.
func TestAValueThatWouldBeReadAsAFlagIsRefused(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	for _, bad := range []string{"--profile=other", "-x", "name with spaces", `name"quoted`, "name;rm -rf /", ""} {
		_, err := PlanVendorAdd(clis[0].ID, VendorCluster{Name: bad}, "/tmp/kubeconfig")
		if err == nil {
			t.Errorf("PlanVendorAdd() accepted %q", bad)
		}
	}
}

// An ordinary cluster name goes through untouched.
func TestAnOrdinaryNameIsPassedThrough(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	plan, err := PlanVendorAdd(clis[0].ID, VendorCluster{
		Name:   "sct-euc3-fi1-prd-svc-01",
		Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
	}, "/tmp/kubeconfig")
	if err != nil {
		t.Fatalf("PlanVendorAdd() = %v", err)
	}
	if !contains(plan.Args, "sct-euc3-fi1-prd-svc-01") {
		t.Errorf("argv = %v, want the cluster name in it", plan.Args)
	}
}

// A plan that needs a field the listing did not supply is refused rather than
// run with a gap in it.
func TestAMissingFieldIsRefused(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	for _, cli := range clis {
		plan, err := PlanVendorAdd(cli.ID, VendorCluster{Name: "example"}, "/tmp/kubeconfig")
		if err == nil {
			// This row needs nothing but a name, which is legitimate.
			if len(plan.Args) == 0 {
				t.Errorf("%s: empty argv", cli.ID)
			}
			continue
		}
		if !errors.Is(err, ErrVendorValueMissing) {
			t.Errorf("%s: error = %v, want ErrVendorValueMissing", cli.ID, err)
		}
	}
}

// PodSteer adds no scope of its own — no region, subscription, project or
// profile. The listing runs as the operator's CLI is configured, so the list
// and the add cannot disagree about which account is meant, and the interface
// can say honestly that this is what their CLI can see.
func TestNoScopeIsAddedToAListing(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	for _, cli := range clis {
		plan, err := PlanVendorList(cli.ID)
		if err != nil {
			t.Fatalf("PlanVendorList(%q) = %v", cli.ID, err)
		}
		for _, arg := range plan.Args {
			for _, scope := range []string{"--region", "--subscription", "--project", "--profile"} {
				if strings.HasPrefix(arg, scope) {
					t.Errorf("%s: the listing passes %q; see decision 12", cli.ID, arg)
				}
			}
		}
	}
}

// withoutComments strips Go comments so the check above reads code only.
//
// Deliberately crude — it does not understand a `//` inside a string literal
// — because it errs towards hiding text rather than towards a false failure,
// and the thing being guarded against is a provider name written into logic,
// which no amount of crudeness would conceal.
func withoutComments(source string) string {
	var out strings.Builder
	for {
		block := strings.Index(source, "/*")
		line := strings.Index(source, "//")

		switch {
		case block < 0 && line < 0:
			out.WriteString(source)
			return out.String()
		case block >= 0 && (line < 0 || block < line):
			out.WriteString(source[:block])
			end := strings.Index(source[block:], "*/")
			if end < 0 {
				return out.String()
			}
			source = source[block+end+2:]
		default:
			out.WriteString(source[:line])
			end := strings.Index(source[line:], "\n")
			if end < 0 {
				return out.String()
			}
			source = source[line+end:]
		}
	}
}
