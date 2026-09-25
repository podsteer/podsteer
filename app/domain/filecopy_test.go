package domain_test

import (
	"errors"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

// TestSplitRemotePathRootsTheArchiveAtTheEntryName pins the reason the
// download runs `tar cf - -C dir base` rather than `tar cf - /full/path`:
// the entries have to land under the chosen local directory by their own
// name, not by their whole absolute path.
func TestSplitRemotePathRootsTheArchiveAtTheEntryName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		remote   string
		wantDir  string
		wantBase string
	}{
		{"/etc/nginx/nginx.conf", "/etc/nginx", "nginx.conf"},
		{"/etc/nginx", "/etc", "nginx"},
		{"/etc/nginx/", "/etc", "nginx"},
		{"/var/log", "/var", "log"},
		// Top-level entries have the root as their directory.
		{"/app", "/", "app"},
		// Relative paths resolve against the container's working directory,
		// which tar spells as ".".
		{"config.yaml", ".", "config.yaml"},
		{"logs/app.log", "logs", "app.log"},
		// Cleaning: doubled slashes and dot segments inside the path are
		// tidied rather than refused.
		{"//etc//nginx/./nginx.conf", "/etc/nginx", "nginx.conf"},
		{"/etc/../etc/hosts", "/etc", "hosts"},
		{"  /tmp/out.txt  ", "/tmp", "out.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.remote, func(t *testing.T) {
			t.Parallel()

			dir, base, err := domain.SplitRemotePath(tt.remote)
			if err != nil {
				t.Fatalf("SplitRemotePath(%q) error = %v", tt.remote, err)
			}
			if dir != tt.wantDir || base != tt.wantBase {
				t.Fatalf("SplitRemotePath(%q) = (%q, %q), want (%q, %q)", tt.remote, dir, base, tt.wantDir, tt.wantBase)
			}
		})
	}
}

// TestSplitRemotePathRefusesWhatCannotBeArchivedByName covers the paths a
// copy must decline rather than attempt: nothing, the root, and anything
// that escapes above a relative starting point.
func TestSplitRemotePathRefusesWhatCannotBeArchivedByName(t *testing.T) {
	t.Parallel()

	for _, remote := range []string{"", "   ", "/", "//", ".", "./", "..", "../etc", "/a\x00b"} {
		t.Run(remote, func(t *testing.T) {
			t.Parallel()

			_, _, err := domain.SplitRemotePath(remote)
			if !errors.Is(err, domain.ErrInvalidRemotePath) {
				t.Fatalf("SplitRemotePath(%q) error = %v, want ErrInvalidRemotePath", remote, err)
			}
		})
	}
}

// TestCleanRemoteDirAllowsTheRoot is the deliberate asymmetry: a download
// of `/` is refused, an upload INTO `/` is not — tar extracts into the root
// exactly as into any other directory.
func TestCleanRemoteDirAllowsTheRoot(t *testing.T) {
	t.Parallel()

	got, err := domain.CleanRemoteDir("/")
	if err != nil || got != "/" {
		t.Fatalf("CleanRemoteDir(\"/\") = (%q, %v), want (\"/\", nil)", got, err)
	}

	got, err = domain.CleanRemoteDir(" /app//data/ ")
	if err != nil || got != "/app/data" {
		t.Fatalf("CleanRemoteDir = (%q, %v), want (\"/app/data\", nil)", got, err)
	}

	if _, err := domain.CleanRemoteDir("  "); !errors.Is(err, domain.ErrInvalidRemotePath) {
		t.Fatalf("CleanRemoteDir(blank) error = %v, want ErrInvalidRemotePath", err)
	}
}

// TestTransferLimitsWithDefaultsFillsOnlyZeroes: a zero value is "no
// opinion", never "no limit".
func TestTransferLimitsWithDefaultsFillsOnlyZeroes(t *testing.T) {
	t.Parallel()

	got := domain.TransferLimits{}.WithDefaults()
	if got.MaxBytes != domain.DefaultTransferMaxBytes || got.MaxEntries != domain.DefaultTransferMaxEntries {
		t.Fatalf("WithDefaults() = %+v, want the defaults", got)
	}

	kept := domain.TransferLimits{MaxBytes: 10, MaxEntries: 2}.WithDefaults()
	if kept.MaxBytes != 10 || kept.MaxEntries != 2 {
		t.Fatalf("WithDefaults() = %+v, want the explicit values kept", kept)
	}
}
