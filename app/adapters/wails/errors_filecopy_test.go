package wails

import (
	"fmt"
	"strings"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
	"github.com/podsteer/podsteer/app/ports"
)

// TestClassifyErrorTarMissingIsNeitherUnreachableNorInternal pins why the
// code exists: the runtime's refusal to start tar arrives as an internal
// error, which every other path here reports as the cluster being
// unreachable — and it was reached perfectly well.
func TestClassifyErrorTarMissingIsNeitherUnreachableNorInternal(t *testing.T) {
	err := fmt.Errorf("copying /etc/hosts: %w: %w", ports.ErrTarMissing, ports.ErrUnreachable)

	code, message := classifyError(err)
	if code != CodeTarMissing {
		t.Fatalf("code %q, want %q", code, CodeTarMissing)
	}
	if !strings.Contains(message, "tar") {
		t.Fatalf("message does not name tar: %q", message)
	}
}

// TestClassifyErrorCommandFailedCarriesStderrVerbatim: the message is what
// the command said, unparaphrased.
func TestClassifyErrorCommandFailedCarriesStderrVerbatim(t *testing.T) {
	const said = "tar: /nope: Cannot stat: No such file or directory"
	err := fmt.Errorf("copying /nope: %w: %s", ports.ErrCommandFailed, said)

	code, message := classifyError(err)
	if code != CodeCommandFailed {
		t.Fatalf("code %q, want %q", code, CodeCommandFailed)
	}
	if !strings.Contains(message, said) {
		t.Fatalf("message %q lost what tar said", message)
	}
}

// TestClassifyErrorTransferLimitNamesTheSettingThatRaisesIt: the operator
// did nothing wrong, and the message says what to change if they meant it.
func TestClassifyErrorTransferLimitNamesTheSettingThatRaisesIt(t *testing.T) {
	err := fmt.Errorf("%w: more than 1073741824 bytes", domain.ErrTransferTooLarge)

	code, message := classifyError(err)
	if code != CodeTransferLimit {
		t.Fatalf("code %q, want %q", code, CodeTransferLimit)
	}
	if !strings.Contains(message, "PODSTEER_COPY_MAX_BYTES") {
		t.Fatalf("message does not name the setting: %q", message)
	}
}

// TestClassifyErrorUnsafeArchiveEntryNamesTheEntry: reported as invalid
// input with the entry's own name, so the operator can see what the
// container tried to plant.
func TestClassifyErrorUnsafeArchiveEntryNamesTheEntry(t *testing.T) {
	err := fmt.Errorf("%w: %q climbs out with \"..\"", domain.ErrUnsafeArchiveEntry, "../../.ssh/authorized_keys")

	code, message := classifyError(err)
	if code != CodeInvalidInput {
		t.Fatalf("code %q, want %q", code, CodeInvalidInput)
	}
	if !strings.Contains(message, ".ssh/authorized_keys") {
		t.Fatalf("message does not name the entry: %q", message)
	}
}
