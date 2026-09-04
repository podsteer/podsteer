package domain_test

import (
	"reflect"
	"testing"

	"github.com/podsteer/podsteer/app/domain"
)

func TestNewProjectionNormalisesItsKeys(t *testing.T) {
	t.Parallel()

	// Order, repetition and surrounding whitespace must not distinguish two
	// projections: the adapter keys its read cache on the rendering, and a
	// list read for one column order has to serve the other.
	first := domain.NewProjection([]string{" team ", "owner", "team", "", "  "})
	second := domain.NewProjection([]string{"owner", "team"})

	if got, want := first.AnnotationKeys(), []string{"owner", "team"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AnnotationKeys() = %v, want %v", got, want)
	}
	if first.String() != second.String() {
		t.Errorf("String() = %q and %q for the same keys", first.String(), second.String())
	}
	if first.String() != "owner,team" {
		t.Errorf("String() = %q, want %q", first.String(), "owner,team")
	}
}

func TestAKeyThatCannotNameAnAnnotationIsDropped(t *testing.T) {
	t.Parallel()

	// A comma would make the rendering ambiguous and whitespace cannot occur
	// in a qualified name, so neither is allowed to reach a cache key.
	projection := domain.NewProjection([]string{"a,b", "with space", "ok"})

	if got, want := projection.AnnotationKeys(), []string{"ok"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AnnotationKeys() = %v, want %v", got, want)
	}
}

func TestTheLastAppliedManifestCannotBeProjected(t *testing.T) {
	t.Parallel()

	// It is the one annotation the watch store strips, so a column of it
	// would read differently depending on which path answered — and it is a
	// copy of the whole manifest besides.
	projection := domain.NewProjection([]string{domain.LastAppliedConfigurationAnnotation})

	if !projection.IsEmpty() {
		t.Fatalf("projection over the last-applied manifest = %v, want empty", projection.AnnotationKeys())
	}
}

func TestProjectionReturnsOnlyTheRequestedKeys(t *testing.T) {
	t.Parallel()

	all := map[string]string{
		"team":  "payments",
		"owner": "alice",
		domain.LastAppliedConfigurationAnnotation: "{...}",
	}

	got := domain.NewProjection([]string{"team", "missing"}).Annotations(all)
	want := map[string]string{"team": "payments"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Annotations() = %v, want %v", got, want)
	}
}

func TestAnEmptyProjectionCarriesNothing(t *testing.T) {
	t.Parallel()

	all := map[string]string{"team": "payments"}

	// Nil, not an empty map: the mappers clone what this returns, and a
	// stripped object and its original must map to EQUAL values, which an
	// empty map on one side and nil on the other would break.
	if got := (domain.Projection{}).Annotations(all); got != nil {
		t.Errorf("zero Projection.Annotations() = %v, want nil", got)
	}
	if got := domain.NewProjection([]string{"absent"}).Annotations(all); got != nil {
		t.Errorf("Annotations() with no key present = %v, want nil", got)
	}
	if got := domain.NewProjection([]string{"team"}).Annotations(nil); got != nil {
		t.Errorf("Annotations(nil) = %v, want nil", got)
	}
}
