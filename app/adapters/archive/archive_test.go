package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// entry is one thing to put in a hand-built archive.
type entry struct {
	name     string
	typeflag byte
	mode     int64
	content  string
	linkname string
}

// buildTar writes entries exactly as given, including names a real tar
// would never produce — that is the point: these are the archives a
// hostile container would send.
func buildTar(t *testing.T, entries ...entry) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{
			Typeflag: typeflag,
			Name:     e.name,
			Mode:     mode,
			Size:     int64(len(e.content)),
			Linkname: e.linkname,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("writing header for %q: %v", e.name, err)
		}
		if _, err := io.WriteString(writer, e.content); err != nil {
			t.Fatalf("writing content for %q: %v", e.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing archive: %v", err)
	}
	return &buf
}

func extract(t *testing.T, archive io.Reader, dest string, limits domain.TransferLimits) (domain.TransferSummary, error) {
	t.Helper()
	return (Local{}).Extract(context.Background(), archive, dest, limits, nil)
}

// TestExtractLandsAnOrdinaryTreeWithItsModes is the happy path: a tree
// with a directory, files and an inside-pointing link comes out as it went
// in, permissions included.
func TestExtractLandsAnOrdinaryTreeWithItsModes(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t,
		entry{name: "app/", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "app/config.yaml", content: "key: value\n", mode: 0o600},
		entry{name: "app/bin/", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "app/bin/run.sh", content: "#!/bin/sh\n", mode: 0o755},
		entry{name: "app/current", typeflag: tar.TypeSymlink, linkname: "config.yaml"},
	)

	summary, err := extract(t, archive, dest, domain.TransferLimits{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	if summary.Files != 2 || summary.Entries != 5 {
		t.Fatalf("summary = %+v, want 2 files across 5 entries", summary)
	}
	if want := int64(len("key: value\n") + len("#!/bin/sh\n")); summary.Bytes != want {
		t.Fatalf("summary.Bytes = %d, want %d", summary.Bytes, want)
	}

	got, err := os.ReadFile(filepath.Join(dest, "app", "config.yaml"))
	if err != nil || string(got) != "key: value\n" {
		t.Fatalf("config.yaml = %q, %v", got, err)
	}

	target, err := os.Readlink(filepath.Join(dest, "app", "current"))
	if err != nil || target != "config.yaml" {
		t.Fatalf("symlink target = %q, %v; want config.yaml", target, err)
	}

	if runtime.GOOS != "windows" {
		assertMode(t, filepath.Join(dest, "app", "config.yaml"), 0o600)
		assertMode(t, filepath.Join(dest, "app", "bin", "run.sh"), 0o755)
	}
}

// TestExtractCreatesParentsAStreamNeverDeclared: tar does not promise a
// directory entry before its contents, and busybox tar routinely omits
// them.
func TestExtractCreatesParentsAStreamNeverDeclared(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t, entry{name: "deep/er/file.txt", content: "x"})

	if _, err := extract(t, archive, dest, domain.TransferLimits{}); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "deep", "er", "file.txt")); err != nil {
		t.Fatalf("file did not land: %v", err)
	}
}

// TestExtractRefusesTraversal covers every shape of name that would land
// outside the chosen directory. Each ends the transfer with the sentinel,
// and nothing appears beside the destination.
func TestExtractRefusesTraversal(t *testing.T) {
	t.Parallel()

	names := []string{
		"../escaped.txt",
		"safe/../../escaped.txt",
		"/etc/escaped.txt",
		"..",
		`..\escaped.txt`,
		`safe\..\..\escaped.txt`,
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			dest := filepath.Join(parent, "dest")
			if err := os.Mkdir(dest, 0o755); err != nil {
				t.Fatal(err)
			}

			archive := buildTar(t, entry{name: name, content: "pwned"})
			_, err := extract(t, archive, dest, domain.TransferLimits{})
			if !errors.Is(err, domain.ErrUnsafeArchiveEntry) {
				t.Fatalf("Extract() error = %v, want ErrUnsafeArchiveEntry", err)
			}

			if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("escaped.txt exists beside the destination: %v", err)
			}
		})
	}
}

// TestExtractRefusesASymlinkPointingOutside: the link itself is the attack
// — a later entry "inside" it would be written through it.
func TestExtractRefusesASymlinkPointingOutside(t *testing.T) {
	t.Parallel()

	targets := []string{"../outside", "/etc", "a/../../outside", `..\outside`}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			dest := t.TempDir()
			archive := buildTar(t,
				entry{name: "link", typeflag: tar.TypeSymlink, linkname: target},
				entry{name: "link/payload", content: "pwned"},
			)

			_, err := extract(t, archive, dest, domain.TransferLimits{})
			if !errors.Is(err, domain.ErrUnsafeArchiveEntry) {
				t.Fatalf("Extract() error = %v, want ErrUnsafeArchiveEntry", err)
			}
			if _, err := os.Lstat(filepath.Join(dest, "link")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the link was created anyway: %v", err)
			}
		})
	}
}

// TestExtractAllowsASymlinkThatStaysInside is the other half: a link that
// resolves under the destination — even one using ".." to get there — is
// an ordinary part of many trees and is kept.
func TestExtractAllowsASymlinkThatStaysInside(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t,
		entry{name: "a/b/", typeflag: tar.TypeDir, mode: 0o755},
		entry{name: "a/target.txt", content: "t"},
		entry{name: "a/b/up", typeflag: tar.TypeSymlink, linkname: "../target.txt"},
	)

	if _, err := extract(t, archive, dest, domain.TransferLimits{}); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a", "b", "up"))
	if err != nil || string(got) != "t" {
		t.Fatalf("reading through the link = %q, %v", got, err)
	}
}

// TestExtractNeverWritesThroughAPreexistingSymlink is the case the name
// checks alone cannot catch: the destination already holds a link to
// somewhere else — left by an earlier download, or planted — and an entry
// named through it must not follow it.
func TestExtractNeverWritesThroughAPreexistingSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}

	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	outside := filepath.Join(parent, "outside")
	for _, dir := range []string{dest, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dest, "escape")); err != nil {
		t.Fatal(err)
	}

	archive := buildTar(t, entry{name: "escape/payload", content: "pwned"})
	if _, err := extract(t, archive, dest, domain.TransferLimits{}); err == nil {
		t.Fatal("Extract() error = nil, want a refusal to write through the link")
	}
	if _, err := os.Stat(filepath.Join(outside, "payload")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("payload landed outside the destination: %v", err)
	}
}

// TestExtractStripsSetuidAndSetgid: the bits that make a file dangerous to
// have lying around are never reproduced, whatever the container had.
func TestExtractStripsSetuidAndSetgid(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}

	dest := t.TempDir()
	// 04755 and 02755 as tar stores them: the perm bits plus the setuid /
	// setgid flags in the same octal word.
	archive := buildTar(t,
		entry{name: "suid", content: "x", mode: 0o4755},
		entry{name: "sgid", content: "x", mode: 0o2755},
		entry{name: "sticky/", typeflag: tar.TypeDir, mode: 0o1777},
	)

	if _, err := extract(t, archive, dest, domain.TransferLimits{}); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	for _, name := range []string{"suid", "sgid", "sticky"} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			t.Fatalf("%s kept a special bit: %v", name, info.Mode())
		}
	}
	assertMode(t, filepath.Join(dest, "suid"), 0o755)
}

// TestExtractStopsAtTheByteCap: the cap is enforced as bytes arrive, and
// the error names the limit that stopped it.
func TestExtractStopsAtTheByteCap(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t,
		entry{name: "one", content: strings.Repeat("a", 60)},
		entry{name: "two", content: strings.Repeat("b", 60)},
	)

	_, err := extract(t, archive, dest, domain.TransferLimits{MaxBytes: 100})
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("Extract() error = %v, want ErrTransferTooLarge", err)
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("error does not say which limit: %q", err)
	}
}

// TestExtractStopsAtTheEntryCap does the same for the count.
func TestExtractStopsAtTheEntryCap(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t,
		entry{name: "a", content: "1"},
		entry{name: "b", content: "2"},
		entry{name: "c", content: "3"},
	)

	_, err := extract(t, archive, dest, domain.TransferLimits{MaxEntries: 2})
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("Extract() error = %v, want ErrTransferTooLarge", err)
	}
	if !strings.Contains(err.Error(), "entries") {
		t.Fatalf("error does not say which limit: %q", err)
	}
}

// TestExtractReportsProgressAsBytesLand pins the callback the UI's
// progress bar is fed from: the deltas sum to the content written.
func TestExtractReportsProgressAsBytesLand(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t, entry{name: "f", content: strings.Repeat("z", 1000)})

	var total int64
	summary, err := (Local{}).Extract(context.Background(), archive, dest, domain.TransferLimits{}, func(n int64) { total += n })
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if total != 1000 || summary.Bytes != 1000 {
		t.Fatalf("progress total = %d, summary bytes = %d, want 1000 and 1000", total, summary.Bytes)
	}
}

// TestExtractSkipsDeviceNodesAndSaysSo: never created, never silently
// dropped either.
func TestExtractSkipsDeviceNodesAndSaysSo(t *testing.T) {
	t.Parallel()

	dest := t.TempDir()
	archive := buildTar(t,
		entry{name: "dev/null", typeflag: tar.TypeChar},
		entry{name: "ok", content: "1"},
	)

	summary, err := extract(t, archive, dest, domain.TransferLimits{})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(summary.Notes) != 1 || !strings.Contains(summary.Notes[0], "dev/null") {
		t.Fatalf("summary.Notes = %v, want one line naming dev/null", summary.Notes)
	}
	if summary.Files != 1 {
		t.Fatalf("summary.Files = %d, want the ordinary file still extracted", summary.Files)
	}
}

// TestExtractStopsWhenCancelled: the operator's Cancel reaches the
// extraction between entries.
func TestExtractStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := t.TempDir()
	archive := buildTar(t, entry{name: "f", content: "1"})

	_, err := (Local{}).Extract(ctx, archive, dest, domain.TransferLimits{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract() error = %v, want context.Canceled", err)
	}
}

// TestExtractRefusesAMissingDestination: the dialog guarantees a directory,
// and this is what happens when a caller does not.
func TestExtractRefusesAMissingDestination(t *testing.T) {
	t.Parallel()

	archive := buildTar(t, entry{name: "f", content: "1"})
	if _, err := extract(t, archive, filepath.Join(t.TempDir(), "missing"), domain.TransferLimits{}); err == nil {
		t.Fatal("Extract() into a missing directory error = nil")
	}
}

// --- Pack --------------------------------------------------------------------

// readTar lists an archive's entries by name, with content for files.
func readTar(t *testing.T, r io.Reader) (map[string]*tar.Header, map[string]string) {
	t.Helper()

	headers := map[string]*tar.Header{}
	contents := map[string]string{}
	reader := tar.NewReader(r)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return headers, contents
		}
		if err != nil {
			t.Fatalf("reading packed archive: %v", err)
		}
		headers[header.Name] = header
		if header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			contents[header.Name] = string(data)
		}
	}
}

func writeFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// TestPackRootsEntriesAtTheBaseName: the container receives `config/…`,
// never the operator's full local path.
func TestPackRootsEntriesAtTheBaseName(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "config")
	writeFile(t, filepath.Join(source, "app.yaml"), "a: 1\n", 0o644)
	writeFile(t, filepath.Join(source, "nested", "b.yaml"), "b: 2\n", 0o600)

	var buf bytes.Buffer
	summary, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{}, nil)
	if err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	headers, contents := readTar(t, &buf)
	for _, want := range []string{"config/", "config/app.yaml", "config/nested/", "config/nested/b.yaml"} {
		if _, ok := headers[want]; !ok {
			t.Errorf("archive lacks %q; has %v", want, keys(headers))
		}
	}
	if contents["config/nested/b.yaml"] != "b: 2\n" {
		t.Errorf("b.yaml content = %q", contents["config/nested/b.yaml"])
	}
	if summary.Files != 2 || summary.Bytes != int64(len("a: 1\n")+len("b: 2\n")) {
		t.Errorf("summary = %+v", summary)
	}

	// Nothing about this machine goes into the container.
	for name, header := range headers {
		if header.Uname != "" || header.Gname != "" || header.Uid != 0 || header.Gid != 0 {
			t.Errorf("%s carries local ownership: uid=%d gid=%d uname=%q gname=%q", name, header.Uid, header.Gid, header.Uname, header.Gname)
		}
	}
}

// TestPackASingleFileIsOneEntry: uploading one file sends one entry named
// after it, which is what `tar xf - -C dest` needs to land it as dest/name.
func TestPackASingleFileIsOneEntry(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "hosts")
	writeFile(t, source, "127.0.0.1 localhost\n", 0o644)

	var buf bytes.Buffer
	if _, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{}, nil); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	headers, contents := readTar(t, &buf)
	if len(headers) != 1 || contents["hosts"] != "127.0.0.1 localhost\n" {
		t.Fatalf("archive = %v / %v, want exactly one entry named hosts", keys(headers), contents)
	}
}

// TestPackStripsSetuidAndSetgid mirrors the extraction rule on the way up.
func TestPackStripsSetuidAndSetgid(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("no POSIX mode bits on Windows")
	}

	source := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(source, "tool"), "x", 0o755)
	if err := os.Chmod(filepath.Join(source, "tool"), 0o755|fs.ModeSetuid); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{}, nil); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	headers, _ := readTar(t, &buf)
	if got := headers["bin/tool"].Mode; got != 0o755 {
		t.Fatalf("packed mode = %o, want 0755 with setuid stripped", got)
	}
}

// TestPackNeverFollowsASymlinkOutsideTheSelection: a link inside the
// chosen tree to somewhere outside it is left out and named, never read.
func TestPackNeverFollowsASymlinkOutsideTheSelection(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}

	parent := t.TempDir()
	secret := filepath.Join(parent, "secret.txt")
	writeFile(t, secret, "do not upload", 0o600)

	source := filepath.Join(parent, "upload")
	writeFile(t, filepath.Join(source, "ok.txt"), "fine", 0o644)
	if err := os.Symlink(secret, filepath.Join(source, "leak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret.txt", filepath.Join(source, "leak-relative")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.txt", filepath.Join(source, "alias")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	summary, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{}, nil)
	if err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	headers, contents := readTar(t, &buf)
	if _, ok := headers["upload/leak"]; ok {
		t.Error("an absolute symlink out of the selection was archived")
	}
	if _, ok := headers["upload/leak-relative"]; ok {
		t.Error("a relative symlink out of the selection was archived")
	}
	for _, content := range contents {
		if content == "do not upload" {
			t.Fatal("the file behind the symlink was uploaded")
		}
	}
	if alias, ok := headers["upload/alias"]; !ok || alias.Typeflag != tar.TypeSymlink || alias.Linkname != "ok.txt" {
		t.Errorf("an inside-pointing link was not kept as a link: %+v", alias)
	}
	if len(summary.Notes) != 2 {
		t.Errorf("summary.Notes = %v, want both skipped links named", summary.Notes)
	}
}

// TestPackFollowsTheChosenPathItself: the one link that IS followed is the
// selection — the operator picked what it points at.
func TestPackFollowsTheChosenPathItself(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}

	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	writeFile(t, filepath.Join(real, "f.txt"), "1", 0o644)
	link := filepath.Join(parent, "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := (Local{}).Pack(context.Background(), &buf, link, domain.TransferLimits{}, nil); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}
	headers, _ := readTar(t, &buf)
	if _, ok := headers["real/f.txt"]; !ok {
		t.Fatalf("archive = %v, want the resolved tree", keys(headers))
	}
}

// TestPackStopsAtTheCaps applies both limits before a byte of the
// offending entry is read.
func TestPackStopsAtTheCaps(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "big")
	writeFile(t, filepath.Join(source, "a"), strings.Repeat("a", 60), 0o644)
	writeFile(t, filepath.Join(source, "b"), strings.Repeat("b", 60), 0o644)

	var buf bytes.Buffer
	_, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{MaxBytes: 100}, nil)
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("Pack() over the byte cap error = %v, want ErrTransferTooLarge", err)
	}

	buf.Reset()
	_, err = (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{MaxEntries: 2}, nil)
	if !errors.Is(err, domain.ErrTransferTooLarge) {
		t.Fatalf("Pack() over the entry cap error = %v, want ErrTransferTooLarge", err)
	}
}

// TestPackRoundTripsThroughExtract: what Pack writes, Extract reads back
// identically — the two halves are what `kubectl cp` relies on tar for.
func TestPackRoundTripsThroughExtract(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "tree")
	writeFile(t, filepath.Join(source, "a.txt"), "A", 0o644)
	writeFile(t, filepath.Join(source, "d", "b.txt"), "B", 0o600)

	var buf bytes.Buffer
	if _, err := (Local{}).Pack(context.Background(), &buf, source, domain.TransferLimits{}, nil); err != nil {
		t.Fatalf("Pack() error = %v", err)
	}

	dest := t.TempDir()
	summary, err := (Local{}).Extract(context.Background(), &buf, dest, domain.TransferLimits{}, nil)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if summary.Files != 2 {
		t.Fatalf("summary.Files = %d, want 2", summary.Files)
	}
	got, err := os.ReadFile(filepath.Join(dest, "tree", "d", "b.txt"))
	if err != nil || string(got) != "B" {
		t.Fatalf("b.txt = %q, %v", got, err)
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", path, got, want)
	}
}

func keys(m map[string]*tar.Header) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
