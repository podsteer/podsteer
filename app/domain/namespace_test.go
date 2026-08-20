package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"podsteer/app/domain"
)

func TestNewNamespaceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		want    domain.NamespaceName
		wantErr error
	}{
		{name: "simple", raw: "default", want: "default"},
		{name: "with hyphens", raw: "kube-system", want: "kube-system"},
		{name: "digits", raw: "team-42", want: "team-42"},
		// A blank name is the cross-namespace query, not a validation failure.
		{name: "blank means all", raw: "", want: domain.NamespaceAll},
		{name: "whitespace means all", raw: "   ", want: domain.NamespaceAll},
		{name: "uppercase", raw: "Default", wantErr: domain.ErrInvalidNamespaceName},
		{name: "leading hyphen", raw: "-system", wantErr: domain.ErrInvalidNamespaceName},
		{name: "trailing hyphen", raw: "system-", wantErr: domain.ErrInvalidNamespaceName},
		{name: "underscore", raw: "kube_system", wantErr: domain.ErrInvalidNamespaceName},
		{name: "too long", raw: strings.Repeat("a", 64), wantErr: domain.ErrInvalidNamespaceName},
		{name: "at the limit", raw: strings.Repeat("a", 63), want: domain.NamespaceName(strings.Repeat("a", 63))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.NewNamespaceName(test.raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewNamespaceName(%q) error = %v, want %v", test.raw, err, test.wantErr)
			}
			if test.wantErr == nil && got != test.want {
				t.Errorf("NewNamespaceName(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestNamespaceNameHelpers(t *testing.T) {
	t.Parallel()

	if !domain.NamespaceAll.IsAll() {
		t.Error("NamespaceAll.IsAll() = false, want true")
	}
	if domain.NamespaceAll.String() != "" {
		t.Error("NamespaceAll must render as the empty string the list APIs expect")
	}
	if got := domain.NamespaceAll.OrDefault(); got != domain.NamespaceDefault {
		t.Errorf("OrDefault() = %q, want %q", got, domain.NamespaceDefault)
	}
	if got := domain.NamespaceName("platform").OrDefault(); got != "platform" {
		t.Errorf("OrDefault() = %q, want %q", got, "platform")
	}
}

// A namespace object always has a concrete name; only a *query* may be blank.
func TestNewNamespaceRejectsBlankName(t *testing.T) {
	t.Parallel()

	if _, err := domain.NewNamespace("", domain.NamespacePhaseActive, time.Time{}); !errors.Is(err, domain.ErrInvalidNamespaceName) {
		t.Errorf("NewNamespace(\"\") error = %v, want %v", err, domain.ErrInvalidNamespaceName)
	}
}

func TestNewNamespacePhaseFallsBackToUnknown(t *testing.T) {
	t.Parallel()

	tests := map[string]domain.NamespacePhase{
		"Active":      domain.NamespacePhaseActive,
		"Terminating": domain.NamespacePhaseTerminating,
		"  Active  ":  domain.NamespacePhaseActive,
		"":            domain.NamespacePhaseUnknown,
		"Draining":    domain.NamespacePhaseUnknown,
	}

	for raw, want := range tests {
		if got := domain.NewNamespacePhase(raw); got != want {
			t.Errorf("NewNamespacePhase(%q) = %q, want %q", raw, got, want)
		}
	}
}
