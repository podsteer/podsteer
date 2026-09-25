package config_test

import (
	"testing"

	"github.com/podsteer/podsteer/app/config"
	"github.com/podsteer/podsteer/app/domain"
)

// TestFileCopyLimitsDefaultToTheDomainsAndReadFromEnv: a ceiling exists
// whether or not anybody set one, and the variables only move it.
func TestFileCopyLimitsDefaultToTheDomainsAndReadFromEnv(t *testing.T) {
	loaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.FileCopy.MaxBytes != domain.DefaultTransferMaxBytes || loaded.FileCopy.MaxEntries != domain.DefaultTransferMaxEntries {
		t.Fatalf("FileCopy = %+v, want the domain's defaults", loaded.FileCopy)
	}

	t.Setenv("PODSTEER_COPY_MAX_BYTES", "5368709120")
	t.Setenv("PODSTEER_COPY_MAX_ENTRIES", "250000")
	raised, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if raised.FileCopy.MaxBytes != 5368709120 || raised.FileCopy.MaxEntries != 250000 {
		t.Fatalf("FileCopy = %+v, want the raised ceilings", raised.FileCopy)
	}
}

// TestAMalformedCopyLimitIsAnErrorRatherThanASilentDefault follows the
// rule the rest of this package holds: somebody who writes
// PODSTEER_COPY_MAX_BYTES=1G needs to be told, not quietly capped at a
// gigabyte they did not ask for.
func TestAMalformedCopyLimitIsAnErrorRatherThanASilentDefault(t *testing.T) {
	t.Setenv("PODSTEER_COPY_MAX_BYTES", "1G")
	if _, err := config.Load(); err == nil {
		t.Fatal("a malformed byte limit was accepted")
	}

	t.Setenv("PODSTEER_COPY_MAX_BYTES", "0")
	if _, err := config.Load(); err == nil {
		t.Fatal("a zero byte limit was accepted — zero would mean no transfer at all")
	}

	t.Setenv("PODSTEER_COPY_MAX_BYTES", "1024")
	t.Setenv("PODSTEER_COPY_MAX_ENTRIES", "-1")
	if _, err := config.Load(); err == nil {
		t.Fatal("a negative entry limit was accepted")
	}
}
