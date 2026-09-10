package domain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE PROTOCOL TEST, and it runs the script for real.
//
// ProbeCommand's twin asserts that what the script emits is what the parser
// reads, from rendered records. This goes further because it can: the script
// is POSIX sh, the machine running the tests has one, and a real directory
// exercises the parts a hand-written fixture would quietly get right — the
// three globs, the dangling symlink, the name with spaces in it.
//
// IT ALSO TESTS PORTABILITY BY ACCIDENT AND ON PURPOSE. /bin/sh is bash in
// POSIX mode on a developer's Mac and dash on the Linux container the gate
// runs in, and `stat -c` exists on one and not the other. A script that
// depended on either would fail in CI or fail here, which is exactly the
// discipline the far end needs: the image is as likely to be busybox as
// Debian.
func TestListScriptAndParserAgreeOnTheProtocol(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "plain.txt"), "hello")
	mustWrite(t, filepath.Join(dir, "a name with spaces.log"), "x")
	mustWrite(t, filepath.Join(dir, ".hidden"), "x")
	mustWrite(t, filepath.Join(dir, "..double"), "x")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink("subdir", filepath.Join(dir, "link-to-dir")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A DANGLING SYMLINK MUST STILL BE LISTED. `test -e` follows the link and
	// fails on a broken one, which is why the script tests `-L` beside it.
	if err := os.Symlink("nowhere-at-all", filepath.Join(dir, "broken-link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	listing := runListing(t, dir)

	byName := map[string]FileEntry{}
	for _, entry := range listing.Entries {
		byName[entry.Name] = entry
	}

	for _, want := range []string{
		"plain.txt", "a name with spaces.log", ".hidden", "..double",
		"subdir", "link-to-dir", "broken-link",
	} {
		if _, ok := byName[want]; !ok {
			t.Errorf("%q is missing from the listing; got %v", want, names(listing.Entries))
		}
	}

	if got := byName["subdir"].Kind; got != EntryDir {
		t.Errorf("subdir kind = %q, want dir", got)
	}
	if got := byName["plain.txt"].Kind; got != EntryFile {
		t.Errorf("plain.txt kind = %q, want file", got)
	}

	// THE ORDERING THAT MAKES THE KIND COLUMN TRUE. `test -d` follows a
	// symlink, so a link to a directory is reported as a directory unless -L
	// is asked first — and the interface would then navigate into it as
	// though it were one, silently leaving the tree.
	link := byName["link-to-dir"]
	if link.Kind != EntrySymlink {
		t.Errorf("link-to-dir kind = %q, want symlink: -L must be tested before -d", link.Kind)
	}
	if !link.ResolvesToDir {
		t.Error("link-to-dir does not report that it resolves to a directory")
	}
	if broken := byName["broken-link"]; broken.Kind != EntrySymlink {
		t.Errorf("broken-link kind = %q, want symlink", broken.Kind)
	}
}

// Sizes are reported or they are absent — never invented. Which of the two
// happens depends on the shell's `stat`, so the assertion is consistency
// rather than presence: this is the same machine-dependence a container image
// creates, tested where it can be.
func TestSizesAreEitherReportedOrAbsentButNeverInvented(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "five.txt"), "12345")

	listing := runListing(t, dir)
	entry := find(t, listing, "five.txt")

	if listing.SizeSource == "stat" {
		if !entry.SizeKnown || entry.Size != 5 {
			t.Errorf("size = %d (known %v), want 5", entry.Size, entry.SizeKnown)
		}
		return
	}

	if entry.SizeKnown {
		t.Error("a size was reported with no stat to have produced it")
	}
	if !containsNote(listing.Notes, "no stat") {
		t.Errorf("notes = %v, want one saying why sizes are missing", listing.Notes)
	}
}

// A NAME PRINTED WITH echo IS A NAME RENAMED. dash's echo interprets
// backslash escapes, so a file genuinely called `a\tb` would arrive as one
// containing a tab — a different file, downloaded without complaint.
func TestABackslashInANameSurvivesListing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	const awkward = `back\tslash`
	mustWrite(t, filepath.Join(dir, awkward), "x")

	listing := runListing(t, dir)
	find(t, listing, awkward)

	if strings.Contains(listScript(dir), `echo "$`) {
		t.Error("the script echoes a variable; printf is the only safe way to print a name")
	}
}

// The path is operator input travelling into a shell. It is single-quoted the
// only way sh allows, and bound to a variable once.
func TestListCommandQuotesThePath(t *testing.T) {
	t.Parallel()

	command := ListCommand(`/tmp/'; rm -rf / ; '`)

	if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-c" {
		t.Fatalf("command = %v, want one /bin/sh -c invocation", command)
	}
	if !strings.Contains(command[2], `'\''`) {
		t.Error("the path is not escaped the way single quotes require")
	}
	if strings.Contains(command[2], "rm -rf / ; '\n") {
		t.Error("part of the path escaped its quoting and became a statement")
	}
}

// The root is listable and is NOT downloadable, and both are deliberate: a
// tar of / is never what anybody meant, and / is the first place anybody
// looks. Asserted together so the divergence reads as a decision.
func TestTheRootIsListableThoughItIsNotDownloadable(t *testing.T) {
	t.Parallel()

	if _, err := CleanListPath("/"); err != nil {
		t.Errorf("CleanListPath(\"/\") = %v, want the root allowed", err)
	}
	if _, _, err := SplitRemotePath("/"); err == nil {
		t.Error("SplitRemotePath(\"/\") allowed the root; the divergence above is only a decision if this still refuses")
	}
}

func TestAPathMustBeAbsoluteAndFreerOfNulBytes(t *testing.T) {
	t.Parallel()

	for _, bad := range []string{"", "   ", "relative/path", "/tmp/\x00/etc"} {
		if _, err := CleanListPath(bad); !errors.Is(err, ErrInvalidListPath) {
			t.Errorf("CleanListPath(%q) = %v, want ErrInvalidListPath", bad, err)
		}
	}
}

// A container that printed more than it was told to must not be able to
// enlarge a listing by saying so.
func TestTheCapIsEnforcedAgainstTheContainersOwnOutput(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("tools 1 1\n")
	for i := 0; i < ListingEntryCap+500; i++ {
		b.WriteString("e file 1 1 - 0\n")
		b.WriteString("n file\n")
	}
	b.WriteString("end 9999\n")

	listing, ok, err := ParseListOutput(b.String(), "/tmp")
	if !ok || err != nil {
		t.Fatalf("ParseListOutput() = %v, %v", ok, err)
	}
	if len(listing.Entries) != ListingEntryCap {
		t.Errorf("entries = %d, want the cap of %d", len(listing.Entries), ListingEntryCap)
	}
	if !listing.Truncated {
		t.Error("a listing that stopped did not say so")
	}
}

// Output that is not a listing is refused whole. Nothing is assumed from it.
func TestOutputThatIsNotAListingIsRefused(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"total 48\ndrwxr-xr-x 2 root root 4096 Sep 10 09:40 etc\n",
		"e file\nn x\n",
		"n orphan\n",
	} {
		if _, ok, err := ParseListOutput(raw, "/tmp"); ok || !errors.Is(err, ErrListingUnreadable) {
			t.Errorf("ParseListOutput(%q) = %v, %v; want ErrListingUnreadable", raw, ok, err)
		}
	}
}

// A name that is not valid text is LISTED and MARKED. Hiding it would
// misrepresent the directory; acting on it would download a different file,
// because Go's JSON encoder replaces the invalid bytes silently.
func TestANameThatIsNotValidTextIsListedAndMarked(t *testing.T) {
	t.Parallel()

	raw := "tools 0 0\ne file - - - 0\nn caf\xc3\xa9-ok\ne file - - - 0\nn bad\xff\xfename\nend 2\n"
	listing, ok, err := ParseListOutput(raw, "/tmp")
	if !ok || err != nil {
		t.Fatalf("ParseListOutput() = %v, %v", ok, err)
	}

	for _, entry := range listing.Entries {
		want := !strings.Contains(entry.Name, "�") && entry.Name != "bad\xff\xfename"
		if entry.NameReadable != want {
			t.Errorf("%q readable = %v, want %v", entry.Name, entry.NameReadable, want)
		}
	}
}

// An entry refused for its name is SAID, never silently dropped: a directory
// that quietly lost an entry is a directory misrepresented.
func TestARefusedNameIsCounted(t *testing.T) {
	t.Parallel()

	listing, ok, err := ParseListOutput("tools 1 1\nskipped newline\ne file 1 1 - 0\nn fine\nend 1\n", "/tmp")
	if !ok || err != nil {
		t.Fatalf("ParseListOutput() = %v, %v", ok, err)
	}
	if !containsNote(listing.Notes, "newline") {
		t.Errorf("notes = %v, want one naming the refusal", listing.Notes)
	}
}

func TestDirectoriesSortFirst(t *testing.T) {
	t.Parallel()

	entries := []FileEntry{
		{Name: "b-file", Kind: EntryFile},
		{Name: "a-file", Kind: EntryFile},
		{Name: "z-dir", Kind: EntryDir},
		{Name: "m-link", Kind: EntrySymlink, ResolvesToDir: true},
	}
	SortListing(entries)

	if got := names(entries); got[0] != "m-link" || got[1] != "z-dir" {
		t.Errorf("order = %v, want directories and directory links first", got)
	}
}

// --- helpers ---------------------------------------------------------------

func runListing(t *testing.T, dir string) DirectoryListing {
	t.Helper()

	command := ListCommand(dir)
	output, err := exec.Command(command[0], command[1:]...).Output()
	if err != nil {
		t.Fatalf("running the listing script: %v", err)
	}

	listing, ok, parseErr := ParseListOutput(string(output), dir)
	if !ok || parseErr != nil {
		t.Fatalf("ParseListOutput() = %v, %v\nscript output:\n%s", ok, parseErr, output)
	}
	return listing
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func find(t *testing.T, listing DirectoryListing, name string) FileEntry {
	t.Helper()
	for _, entry := range listing.Entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("%q not in the listing; got %v", name, names(listing.Entries))
	return FileEntry{}
}

func names(entries []FileEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

func containsNote(notes []string, fragment string) bool {
	for _, note := range notes {
		if strings.Contains(note, fragment) {
			return true
		}
	}
	return false
}
