//go:build !windows

package vendorcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// The runner is tested against REAL PROCESSES — shell scripts written into a
// temporary directory that is put on PATH — because every rule in it is about
// what a process does: exits non-zero, waits for stdin, outlives its bound,
// spawns a child, prints more than anybody wants. A fake exec.Cmd would assert
// the code's opinion of those things rather than the things.
//
// The binary name comes from the shipped table, so this file names no provider
// either.

// listing is a row of the table together with output it will accept.
//
// FOUND BY TRYING, NOT BY KNOWING. This package must not know which provider
// prints which shape — that is the whole point of the table — so the
// candidates below are offered to each row and the first that parses wins. A
// row added later with a shape nobody here anticipated skips these tests
// rather than failing them falsely.
type listing struct {
	provider string
	two      string
	none     string
}

func listingFor(t *testing.T) listing {
	t.Helper()

	candidates := []listing{
		{two: `{"clusters":["alpha","beta"]}`, none: `{"clusters":[]}`},
		{
			two:  `[{"name":"alpha","resourceGroup":"rg","location":"eu"},{"name":"beta","resourceGroup":"rg","location":"eu"}]`,
			none: `[]`,
		},
	}

	clis, err := domain.VendorCLIs()
	if err != nil || len(clis) == 0 {
		t.Fatalf("VendorCLIs() = %v, %d rows", err, len(clis))
	}

	for _, cli := range clis {
		for _, candidate := range candidates {
			got, err := domain.ParseVendorList(cli.ID, []byte(candidate.two))
			if err == nil && len(got) == 2 {
				candidate.provider = cli.ID
				return candidate
			}
		}
	}
	t.Fatal("no shipped row accepts any listing shape this test knows")
	return listing{}
}

// fakeCLI writes a script under the name a row's binary has and puts it FIRST
// on PATH. body is a shell script without its shebang.
//
// The rest of PATH is kept: these scripts use `sleep` and `head`, and a PATH
// holding only the fake would have them fail for reasons that have nothing to
// do with what is being tested. The temporary directory comes first, so the
// fake shadows any real CLI on the machine running the suite.
func fakeCLI(t *testing.T, provider, body string) string {
	t.Helper()

	binary := binaryOf(t, provider)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, binary), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing the fake CLI: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func binaryOf(t *testing.T, provider string) string {
	t.Helper()

	clis, err := domain.VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() = %v", err)
	}
	for _, cli := range clis {
		if cli.ID == provider {
			return cli.Binary
		}
	}
	t.Fatalf("no row %q", provider)
	return ""
}

func TestAMissingCLIStartsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	clis, _ := domain.VendorCLIs()
	_, err := New(nil).ListClusters(context.Background(), clis[0].ID)

	if !errors.Is(err, ports.ErrVendorCLIMissing) {
		t.Fatalf("error = %v, want ErrVendorCLIMissing", err)
	}
	// PodSteer never obtains one: "never installed, only found".
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("something appeared in %s; nothing here may fetch a binary", dir)
	}
}

func TestAListingIsRead(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, "cat <<'JSON'\n"+shape.two+"\nJSON")

	list, err := New(nil).ListClusters(context.Background(), shape.provider)
	if err != nil {
		t.Fatalf("ListClusters() = %v", err)
	}

	if list.Status != domain.VendorListed {
		t.Errorf("status = %q, want listed", list.Status)
	}
	if len(list.Clusters) != 2 {
		t.Fatalf("clusters = %+v, want two", list.Clusters)
	}
}

// A CLI that declines is a STATE, not a failed call: the interface shows its
// words and offers to try again. Only a failure to run it is an error.
func TestACLIThatDeclinesCarriesItsOwnWords(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, `echo "sign in first, then try again" >&2; exit 1`)

	list, err := New(nil).ListClusters(context.Background(), shape.provider)
	if err != nil {
		t.Fatalf("ListClusters() = %v, want a declined listing rather than an error", err)
	}

	if list.Status != domain.VendorDeclined {
		t.Errorf("status = %q, want declined", list.Status)
	}
	if !strings.Contains(list.Reason, "sign in first") {
		t.Errorf("reason = %q, want the CLI's own words verbatim", list.Reason)
	}
}

// Chatter on stderr from a run that SUCCEEDED is not evidence of failure.
// These CLIs write update notices and deprecation warnings there constantly.
func TestStderrFromASuccessfulRunIsNotAFailure(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, "echo 'a new release is available' >&2\ncat <<'JSON'\n"+shape.none+"\nJSON")

	list, err := New(nil).ListClusters(context.Background(), shape.provider)
	if err != nil {
		t.Fatalf("ListClusters() = %v", err)
	}
	if list.Status != domain.VendorListed {
		t.Errorf("status = %q, want listed — stderr is not data", list.Status)
	}
}

// A CLI that asks a question gets EOF rather than a wait. An inherited stdin
// would leave it waiting for a terminal that does not exist.
func TestACLIThatReadsStdinDoesNotHang(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, "read answer\ncat <<'JSON'\n"+shape.none+"\nJSON")

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = New(nil).ListClusters(context.Background(), shape.provider)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a CLI reading stdin blocked the call; stdin must be closed rather than inherited")
	}
}

// The timeout stops the whole tree, not just the leader. These CLIs shell out
// to credential helpers and browsers, and killing only the parent leaves those
// running with nothing waiting for them.
func TestATimeoutStopsTheChildrenToo(t *testing.T) {
	shape := listingFor(t)
	marker := filepath.Join(t.TempDir(), "grandchild-survived")

	// A child that outlives its parent, and a parent that would sit there for
	// half a minute. Killing only the leader leaves the first one running.
	fakeCLI(t, shape.provider, "sh -c 'sleep 3; touch "+marker+"' &\nsleep 30\n")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := New(nil).ListClusters(ctx, shape.provider)
	if err == nil {
		t.Fatal("expected the run to be stopped")
	}

	time.Sleep(4 * time.Second)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("a grandchild outlived the run; the process GROUP must be signalled")
	}
}

// More output than a listing can sanely be is refused UNDECODED. Decoding it
// to find out how big it was is how a desktop application runs out of memory.
func TestAnEnormousListingIsRefusedRatherThanDecoded(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, `head -c 6000000 /dev/zero | tr '\0' 'x'`)

	_, err := New(nil).ListClusters(context.Background(), shape.provider)
	if !errors.Is(err, ports.ErrVendorCLIUnreadable) {
		t.Fatalf("error = %v, want ErrVendorCLIUnreadable", err)
	}
}

func TestOutputOfTheWrongShapeIsUnreadable(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, `echo 'not json'`)

	_, err := New(nil).ListClusters(context.Background(), shape.provider)
	if !errors.Is(err, ports.ErrVendorCLIUnreadable) {
		t.Fatalf("error = %v, want ErrVendorCLIUnreadable", err)
	}
}

// The kubeconfig the CLI writes goes into a directory PodSteer made and
// removes — on success, and on every failure.
func TestTheTemporaryKubeconfigIsRemoved(t *testing.T) {
	shape := listingFor(t)
	// The fake records WHERE it was told to write, so the test can assert that
	// place is gone afterwards. It handles both ways a row can point a CLI at
	// a file — a flag, or KUBECONFIG — because which one this row uses is the
	// table's business and not this test's.
	record := filepath.Join(t.TempDir(), "where")
	fakeCLI(t, shape.provider, `
for arg in "$@"; do
  case "$prev" in --kubeconfig|--file) echo written > "$arg"; echo "$arg" > `+record+`;; esac
  prev="$arg"
done
if [ -n "$KUBECONFIG" ]; then echo written > "$KUBECONFIG"; echo "$KUBECONFIG" > `+record+`; fi
`)

	text, err := New(nil).WriteKubeconfig(context.Background(), shape.provider, domain.VendorCluster{
		Name:   "alpha",
		Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
	})
	if err != nil {
		t.Fatalf("WriteKubeconfig() = %v", err)
	}
	if !strings.Contains(text, "written") {
		t.Errorf("text = %q, want what the CLI wrote", text)
	}

	where, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the fake CLI recorded no path: %v", err)
	}
	used := strings.TrimSpace(string(where))
	if _, err := os.Stat(used); !os.IsNotExist(err) {
		t.Errorf("%s still exists; the temporary kubeconfig must be gone when the call returns", used)
	}
}

// A CLI that exits cleanly and writes nothing is the CLI's contract broken,
// and must not read as "your cluster could not be added".
func TestACleanExitWithNoKubeconfigIsUnreadable(t *testing.T) {
	shape := listingFor(t)
	fakeCLI(t, shape.provider, `exit 0`)

	_, err := New(nil).WriteKubeconfig(context.Background(), shape.provider, domain.VendorCluster{
		Name:   "alpha",
		Params: map[string]string{"resourceGroup": "rg", "location": "eu"},
	})
	if !errors.Is(err, ports.ErrVendorCLIUnreadable) {
		t.Fatalf("error = %v, want ErrVendorCLIUnreadable", err)
	}
}

// A binary anybody on the machine can rewrite is not run at all.
func TestABinaryInAWorldWritableDirectoryIsRefused(t *testing.T) {
	shape := listingFor(t)
	dir := fakeCLI(t, shape.provider, "cat <<'JSON'\n"+shape.none+"\nJSON")
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	_, err := New(nil).ListClusters(context.Background(), shape.provider)
	if !errors.Is(err, ports.ErrVendorCLIMissing) {
		t.Fatalf("error = %v, want a refusal naming the path", err)
	}
	if !strings.Contains(err.Error(), "writable by anybody") {
		t.Errorf("error = %v, want it to say why", err)
	}
}

// Providers is a PATH lookup and nothing else: opening a dialog is not a
// request to run anything.
func TestProvidersStartsNoProcess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "it-ran")

	clis, _ := domain.VendorCLIs()
	path := filepath.Join(dir, clis[0].Binary)
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatalf("writing the fake CLI: %v", err)
	}
	t.Setenv("PATH", dir)

	found := New(nil).Providers()

	if len(found) == 0 {
		t.Fatal("Providers() found nothing")
	}
	if !found[0].Installed || found[0].Path == "" {
		t.Errorf("row = %+v, want it found with a path", found[0])
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("Providers() ran the binary; it must only look for it")
	}
}

// A row whose CLI PRINTS the kubeconfig writes nothing to disk at all, which
// is better than a temporary file however carefully that file is handled: the
// cost decision 12 names and accepts is simply not paid.
func TestAPrintedKubeconfigNeverReachesDisk(t *testing.T) {
	printer := printingRow(t)
	fakeCLI(t, printer, "echo 'apiVersion: v1'\necho 'kind: Config'")

	before := tempKubeconfigDirs(t)

	text, err := New(nil).WriteKubeconfig(context.Background(), printer, domain.VendorCluster{
		Name:   "alpha",
		Params: map[string]string{"resourceGroup": "rg", "location": "eu", "region": "lon1"},
	})
	if err != nil {
		t.Fatalf("WriteKubeconfig() = %v", err)
	}

	if !strings.Contains(text, "kind: Config") {
		t.Errorf("text = %q, want what the CLI printed", text)
	}
	if after := tempKubeconfigDirs(t); after != before {
		t.Errorf("temporary kubeconfig directories went from %d to %d; a printing row must make none", before, after)
	}
}

// A printing row that prints nothing is the CLI's contract broken, and must
// not read as "your cluster could not be added".
func TestAPrintingCLIThatPrintsNothingIsUnreadable(t *testing.T) {
	printer := printingRow(t)
	fakeCLI(t, printer, `exit 0`)

	_, err := New(nil).WriteKubeconfig(context.Background(), printer, domain.VendorCluster{
		Name:   "alpha",
		Params: map[string]string{"region": "lon1", "location": "eu", "resourceGroup": "rg"},
	})
	if !errors.Is(err, ports.ErrVendorCLIUnreadable) {
		t.Fatalf("error = %v, want ErrVendorCLIUnreadable", err)
	}
}

// printingRow finds a row whose CLI prints the kubeconfig, or skips.
func printingRow(t *testing.T) string {
	t.Helper()

	clis, err := domain.VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() = %v", err)
	}
	for _, cli := range clis {
		prints, err := domain.VendorPrintsKubeconfig(cli.ID)
		if err == nil && prints {
			return cli.ID
		}
	}
	t.Skip("no shipped row prints its kubeconfig")
	return ""
}

// tempKubeconfigDirs counts the directories this package would have made.
func tempKubeconfigDirs(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("reading the temporary directory: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "podsteer-kubeconfig-") {
			count++
		}
	}
	return count
}
