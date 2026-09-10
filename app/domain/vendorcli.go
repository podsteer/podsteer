// Driving a cloud vendor's own CLI, which the operator already has.
//
// WHAT THIS IS NOT. It is not cloud cluster discovery as Lens sells it, and
// decision 11 refuses that in full: no provider SDK, no cloud credential read
// by PodSteer, no new host contacted by this process. What it is instead is
// the middle path decision 12 records — PodSteer asks a CLI the operator
// installed and already signed in to which clusters it can see, and asks it to
// write the kubeconfig entry for the one they choose. Every call to a provider
// is made by that CLI, as that operator, exactly as an exec credential plugin
// already works today.
//
// THE PLAN IS MADE HERE AND EXECUTED ELSEWHERE. The command and the parser are
// one protocol — the same rule reachability's ProbeCommand and
// ParseProbeOutput follow — so the argv, the shape its output must have and
// the refusals live together in the domain with tests that need no binary
// installed anywhere. The adapter runs what it is handed and decides nothing.
//
// AND THE PROVIDERS ARE DATA. Every provider-specific fact — the binary, the
// subcommands, the output shape, the label an operator reads — is one object
// in vendorclis.json, embedded at build time. NO PROVIDER IS NAMED IN GO
// SOURCE, and a test in this package greps these files to keep it that way.
// Adding, renaming or removing one is a diff to a data file rather than to
// logic, which is the point: nobody has to remember a rule that the compiler
// and a test can hold instead.
package domain

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed vendorclis.json
var vendorCLIsRaw []byte

// The bounds a run is given. On the plan rather than in the adapter, so one
// place decides them and a test can read them — the rule ProbeTimeout follows.
const (
	// VendorListTimeout bounds a listing. Generous, because these CLIs
	// authenticate before they answer and a cold credential cache can spend
	// several seconds on that alone; short enough that a wedged one cannot
	// hold a dialog open indefinitely.
	VendorListTimeout = 30 * time.Second

	// VendorAddTimeout bounds writing a kubeconfig entry, which is the same
	// work plus one more round trip.
	VendorAddTimeout = 60 * time.Second

	// VendorOutputLimit is the most stdout a run may produce before it is
	// refused UNDECODED. A CLI that prints a hundred megabytes has not
	// answered the question, and decoding it to find that out is how a
	// desktop application runs out of memory.
	VendorOutputLimit = 4 << 20
)

// Errors a plan can refuse with, before anything is executed.
var (
	// ErrVendorUnknown means no row in the table has that id.
	ErrVendorUnknown = errors.New("no such cloud CLI")

	// ErrVendorValueUnsafe means a value the CLI itself printed cannot be
	// passed back to it as an argument. See bindPlaceholder.
	ErrVendorValueUnsafe = errors.New("that value cannot be passed to a command")

	// ErrVendorValueMissing means the plan needs a field the listing did not
	// supply — an entry whose add command wants a resource group, on a row
	// that came back without one.
	ErrVendorValueMissing = errors.New("that cluster did not come with everything the command needs")

	// ErrVendorOutputUnreadable means the CLI printed something that is not
	// the shape its row declares. NEVER A PARTIAL LIST: half a list read as
	// a whole one is worse than no list.
	ErrVendorOutputUnreadable = errors.New("could not read what the CLI printed")
)

// VendorCLI is one row of the table, as the rest of the application sees it.
type VendorCLI struct {
	ID    string
	Label string
	// Binary is the executable to look for on this platform.
	Binary string
	// SignInHint is one sentence shown BESIDE the CLI's own words when it
	// declines, never instead of them.
	SignInHint string
}

// VendorCluster is one cluster a CLI reported.
//
// Params carries whatever else that provider needs to name it again — a
// resource group, a location — keyed by the field name its row declared. The
// domain does not know what any of them mean, which is the point.
type VendorCluster struct {
	Name   string
	Params map[string]string
}

// VendorListStatus is what came of asking.
//
// LISTED WITH NOTHING IN IT IS NOT DECLINED, and they are separate values for
// the reason HelmListStatus keeps its own distinctions: an account with no
// clusters and a CLI that would not answer need opposite responses, and a
// single empty list cannot say which happened.
type VendorListStatus string

const (
	VendorListed   VendorListStatus = "listed"
	VendorDeclined VendorListStatus = "declined"
)

// VendorClusterList is one provider's answer.
type VendorClusterList struct {
	Provider string
	Status   VendorListStatus
	Clusters []VendorCluster
	// Reason is the CLI's own words when it declined, verbatim. The vendor's
	// text IS the diagnosis; paraphrasing it would replace something the
	// operator can act on with something PodSteer guessed.
	Reason string
}

// VendorPlan is a command to run, and how to read what it writes.
type VendorPlan struct {
	Binary  string
	Args    []string
	Env     map[string]string
	Timeout time.Duration
	// KubeconfigFlag names the flag that points the CLI at a file to write,
	// and KubeconfigEnv names the variable that does the same. Exactly one is
	// set on an add plan; both are empty on a list plan.
	KubeconfigFlag string
	KubeconfigEnv  string
}

// --- the table -----------------------------------------------------------

type vendorTable struct {
	Version   int                   `json:"version"`
	Providers []vendorCLIDefinition `json:"providers"`
}

type vendorCLIDefinition struct {
	ID     string            `json:"id"`
	Label  string            `json:"label"`
	Binary vendorBinary      `json:"binary"`
	Env    map[string]string `json:"env"`
	List   vendorListSpec    `json:"list"`
	Add    vendorAddSpec     `json:"add"`
	// SignInHint is one sentence; DeclinedHeadings are fragments that choose
	// a HEADING and never change what is shown. See the note on matching.
	SignInHint       string   `json:"signInHint"`
	DeclinedHeadings []string `json:"declinedHeadings"`
}

type vendorBinary struct {
	Default string `json:"default"`
	Windows string `json:"windows"`
}

type vendorListSpec struct {
	Args []string `json:"args"`
	// Items is the JSON key holding the array, or "" when the whole document
	// is the array.
	Items string `json:"items"`
	// Fields maps a field of VendorCluster — "name", or "params.<key>" — to
	// the candidate keys to read it from, first present winning. A candidate
	// of "$self" means the array element IS the value, for a CLI that prints
	// an array of strings.
	Fields map[string][]string `json:"fields"`
}

type vendorAddSpec struct {
	Args   []string        `json:"args"`
	Target vendorAddTarget `json:"target"`
}

type vendorAddTarget struct {
	// Kind is "flag" or "env": some CLIs take a file to write, others only
	// honour KUBECONFIG.
	Kind string `json:"kind"`
	Name string `json:"name"`
}

var (
	vendorOnce  sync.Once
	vendorRows  map[string]vendorCLIDefinition
	vendorOrder []string
	vendorErr   error
)

func loadVendorTable() {
	var table vendorTable
	if err := json.Unmarshal(vendorCLIsRaw, &table); err != nil {
		vendorErr = fmt.Errorf("reading the cloud CLI table: %w", err)
		return
	}

	vendorRows = make(map[string]vendorCLIDefinition, len(table.Providers))
	for _, row := range table.Providers {
		if err := validateVendorRow(row); err != nil {
			vendorErr = err
			return
		}
		vendorRows[row.ID] = row
		vendorOrder = append(vendorOrder, row.ID)
	}
	sort.Strings(vendorOrder)
}

// validateVendorRow refuses a row that could not be run or read.
//
// AT LOAD, NOT AT USE. The table ships inside the binary, so a row that cannot
// work is a build-time mistake; finding it when somebody presses a button
// would make it an operator's problem instead of a test's.
func validateVendorRow(row vendorCLIDefinition) error {
	switch {
	case row.ID == "":
		return errors.New("a cloud CLI row has no id")
	case row.Label == "":
		return fmt.Errorf("cloud CLI %q has no label", row.ID)
	case row.Binary.Default == "":
		return fmt.Errorf("cloud CLI %q names no binary", row.ID)
	case len(row.List.Args) == 0:
		return fmt.Errorf("cloud CLI %q has no list command", row.ID)
	case len(row.Add.Args) == 0:
		return fmt.Errorf("cloud CLI %q has no add command", row.ID)
	case len(row.List.Fields["name"]) == 0:
		return fmt.Errorf("cloud CLI %q does not say how to read a cluster name", row.ID)
	case row.Add.Target.Kind != "flag" && row.Add.Target.Kind != "env":
		return fmt.Errorf("cloud CLI %q has no way to be pointed at a kubeconfig", row.ID)
	case row.Add.Target.Name == "":
		return fmt.Errorf("cloud CLI %q names no kubeconfig target", row.ID)
	}

	// A row that asks a CLI for admin or embedded credentials would put a
	// credential in the temporary kubeconfig this feature writes. Decision 12
	// says it does not, and this is what holds it to that.
	for _, arg := range row.Add.Args {
		if strings.Contains(strings.ToLower(arg), "admin") {
			return fmt.Errorf("cloud CLI %q asks for admin credentials, which decision 12 refuses", row.ID)
		}
	}
	return nil
}

// VendorCLIs returns every row in the table, in a stable order.
func VendorCLIs() ([]VendorCLI, error) {
	vendorOnce.Do(loadVendorTable)
	if vendorErr != nil {
		return nil, vendorErr
	}

	out := make([]VendorCLI, 0, len(vendorOrder))
	for _, id := range vendorOrder {
		row := vendorRows[id]
		out = append(out, VendorCLI{
			ID:         row.ID,
			Label:      row.Label,
			Binary:     row.binaryName(),
			SignInHint: row.SignInHint,
		})
	}
	return out, nil
}

// binaryName is the executable for the platform this build runs on.
func (d vendorCLIDefinition) binaryName() string {
	if runtime.GOOS == "windows" && d.Windows() != "" {
		return d.Windows()
	}
	return d.Binary.Default
}

// Windows returns the platform-specific name, if the row carries one.
func (d vendorCLIDefinition) Windows() string { return d.Binary.Windows }

func vendorRow(id string) (vendorCLIDefinition, error) {
	vendorOnce.Do(loadVendorTable)
	if vendorErr != nil {
		return vendorCLIDefinition{}, vendorErr
	}
	row, ok := vendorRows[id]
	if !ok {
		return vendorCLIDefinition{}, fmt.Errorf("%w: %q", ErrVendorUnknown, id)
	}
	return row, nil
}

// --- the plans -----------------------------------------------------------

// PlanVendorList is the command that asks one CLI what it can see.
//
// NO SCOPE OF PODSTEER'S OWN. No region, subscription, project or profile
// argument is ever added: the listing runs exactly as the operator's CLI is
// configured, so the list and the add can never disagree about which account
// is being talked about, and the interface can say honestly that this is what
// their CLI is configured to see. It is also the boundary that keeps this
// feature small — the moment PodSteer enumerates scopes, it is doing the work
// decision 11 refused.
func PlanVendorList(provider string) (VendorPlan, error) {
	row, err := vendorRow(provider)
	if err != nil {
		return VendorPlan{}, err
	}

	return VendorPlan{
		Binary:  row.binaryName(),
		Args:    append([]string(nil), row.List.Args...),
		Env:     row.Env,
		Timeout: VendorListTimeout,
	}, nil
}

// PlanVendorAdd is the command that writes a kubeconfig entry for one cluster.
//
// The values bound into it are values the CLI ITSELF printed a moment ago —
// never anything typed, and never anything the frontend sent back. Even so
// they are checked, because "it came from the CLI" is a claim about the
// present that a future caller can break.
func PlanVendorAdd(provider string, cluster VendorCluster, kubeconfigPath string) (VendorPlan, error) {
	row, err := vendorRow(provider)
	if err != nil {
		return VendorPlan{}, err
	}
	if kubeconfigPath == "" {
		return VendorPlan{}, errors.New("no kubeconfig path for the CLI to write")
	}

	args := make([]string, 0, len(row.Add.Args)+2)
	for _, arg := range row.Add.Args {
		bound, err := bindPlaceholder(arg, cluster)
		if err != nil {
			return VendorPlan{}, err
		}
		args = append(args, bound)
	}

	plan := VendorPlan{
		Binary:  row.binaryName(),
		Env:     row.Env,
		Timeout: VendorAddTimeout,
	}

	switch row.Add.Target.Kind {
	case "flag":
		plan.KubeconfigFlag = row.Add.Target.Name
		args = append(args, row.Add.Target.Name, kubeconfigPath)
	case "env":
		plan.KubeconfigEnv = row.Add.Target.Name
	}

	plan.Args = args
	return plan, nil
}

// bindPlaceholder replaces `{name}` and `{params.<key>}` in one argument.
//
// THE WHOLE ARGUMENT IS THE PLACEHOLDER OR NONE OF IT IS. An argument like
// `--name={name}` is deliberately not supported: partial substitution is how a
// value ends up glued to a flag it was never meant to modify, and every CLI
// here takes its values as separate arguments anyway.
func bindPlaceholder(arg string, cluster VendorCluster) (string, error) {
	if !strings.HasPrefix(arg, "{") || !strings.HasSuffix(arg, "}") {
		return arg, nil
	}

	key := arg[1 : len(arg)-1]
	var value string
	switch {
	case key == "name":
		value = cluster.Name
	case strings.HasPrefix(key, "params."):
		value = cluster.Params[strings.TrimPrefix(key, "params.")]
	default:
		return "", fmt.Errorf("%w: %q", ErrVendorValueMissing, key)
	}

	if value == "" {
		return "", fmt.Errorf("%w: %q", ErrVendorValueMissing, key)
	}
	if !safeVendorValue(value) {
		return "", fmt.Errorf("%w: %q", ErrVendorValueUnsafe, value)
	}
	return value, nil
}

// safeVendorValue is the conservative charset a bound value must fit.
//
// DEFENCE IN DEPTH, NOT THE DEFENCE. Nothing here is passed to a shell — the
// adapter runs an argv and never `sh -c` — so this is not what stands between
// an operator and a command injection. What it does is refuse two shapes that
// are wrong however they arrive: a value that would be read as a FLAG because
// it begins with a dash, and a value carrying whitespace or quoting that a
// batch-wrapped CLI on Windows cannot be given safely at all.
func safeVendorValue(value string) bool {
	if strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':', r == '/', r == '@', r == '+':
		default:
			return false
		}
	}
	return value != ""
}

// --- reading what a CLI printed ------------------------------------------

// ParseVendorList reads one CLI's listing output against its row's shape.
//
// It returns ErrVendorOutputUnreadable rather than whatever it managed to
// understand. A list that silently lost half its clusters is worse than an
// error: an operator would go looking for the missing one in the wrong place.
func ParseVendorList(provider string, output []byte) ([]VendorCluster, error) {
	row, err := vendorRow(provider)
	if err != nil {
		return nil, err
	}

	items, err := vendorItems(row, output)
	if err != nil {
		return nil, err
	}

	clusters := make([]VendorCluster, 0, len(items))
	for _, item := range items {
		cluster, err := vendorClusterOf(row, item)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, cluster)
	}
	return clusters, nil
}

// vendorItems finds the array a row's output holds.
func vendorItems(row vendorCLIDefinition, output []byte) ([]json.RawMessage, error) {
	if row.List.Items == "" {
		var items []json.RawMessage
		if err := json.Unmarshal(output, &items); err != nil {
			return nil, fmt.Errorf("%w: %s expected an array", ErrVendorOutputUnreadable, row.ID)
		}
		return items, nil
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(output, &document); err != nil {
		return nil, fmt.Errorf("%w: %s expected an object", ErrVendorOutputUnreadable, row.ID)
	}

	raw, ok := document[row.List.Items]
	if !ok {
		// An object without the key is not an empty list: it is output this
		// row does not describe, and reading it as "no clusters" would report
		// an account as empty because a CLI changed its shape.
		return nil, fmt.Errorf("%w: %s printed no %q", ErrVendorOutputUnreadable, row.ID, row.List.Items)
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: %s expected %q to be an array", ErrVendorOutputUnreadable, row.ID, row.List.Items)
	}
	return items, nil
}

// vendorClusterOf reads one element into a cluster, by the row's field map.
func vendorClusterOf(row vendorCLIDefinition, item json.RawMessage) (VendorCluster, error) {
	cluster := VendorCluster{Params: map[string]string{}}

	for field, candidates := range row.List.Fields {
		value, err := vendorField(item, candidates)
		if err != nil {
			return VendorCluster{}, fmt.Errorf("%w: %s: %w", ErrVendorOutputUnreadable, row.ID, err)
		}

		switch {
		case field == "name":
			cluster.Name = value
		case strings.HasPrefix(field, "params."):
			if value != "" {
				cluster.Params[strings.TrimPrefix(field, "params.")] = value
			}
		}
	}

	if cluster.Name == "" {
		return VendorCluster{}, fmt.Errorf("%w: %s printed a cluster with no name", ErrVendorOutputUnreadable, row.ID)
	}
	return cluster, nil
}

// vendorField reads the first candidate key that is present.
//
// FIRST PRESENT WINS, so a row can name a key that was renamed upstream and
// keep the old one behind it — which is the cheapest way to survive a CLI
// changing its output without shipping a new binary.
func vendorField(item json.RawMessage, candidates []string) (string, error) {
	for _, candidate := range candidates {
		if candidate == "$self" {
			var value string
			if err := json.Unmarshal(item, &value); err != nil {
				return "", errors.New("expected a string")
			}
			return value, nil
		}

		var object map[string]json.RawMessage
		if err := json.Unmarshal(item, &object); err != nil {
			return "", errors.New("expected an object")
		}
		raw, ok := object[candidate]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", fmt.Errorf("expected %q to be a string", candidate)
		}
		return value, nil
	}
	// Absent is not an error here: a row may declare an optional field, and
	// the plan refuses later if the add command actually needed it.
	return "", nil
}

// VendorDeclinedHeading picks the heading for a CLI that would not answer.
//
// THE FRAGMENTS CHOOSE A HEADING AND NOTHING ELSE. "Not signed in" is not a
// machine-readable state on any of these CLIs — they differ in exit code and
// wording, and both change on the vendor's schedule — so nothing here depends
// on recognising it. What the operator reads is the CLI's own text either way;
// this only decides whether the sentence above it says "isn't signed in" or
// the blunter "couldn't list your clusters".
func VendorDeclinedHeading(provider, stderr string) (signedOut bool) {
	row, err := vendorRow(provider)
	if err != nil {
		return false
	}

	lower := strings.ToLower(stderr)
	for _, fragment := range row.DeclinedHeadings {
		if fragment != "" && strings.Contains(lower, strings.ToLower(fragment)) {
			return true
		}
	}
	return false
}
