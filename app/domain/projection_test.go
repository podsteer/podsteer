package domain_test

import (
	"reflect"
	"strings"
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

func TestProjectionExpressionsAreCleanedAndOrdered(t *testing.T) {
	t.Parallel()

	projection := domain.Projection{}.WithExpressions([]domain.CustomExpression{
		{ID: "c2", Path: " .status.phase "},
		{ID: "", Path: ".spec.nodeName"},
		{ID: "c1", Path: ""},
		{ID: "bad,id", Path: ".spec.nodeName"},
		{ID: "c0", Path: ".metadata.name"},
	})

	got := projection.Expressions()
	if len(got) != 2 {
		t.Fatalf("expressions = %v, want the two that name something", got)
	}
	// Sorted by id, so two projections naming the same columns in a different
	// order render the same cache key.
	if got[0].ID != "c0" || got[1].ID != "c2" {
		t.Errorf("order = %v, want sorted by id", got)
	}
	if got[1].Path != ".status.phase" {
		t.Errorf("path = %q, want it trimmed", got[1].Path)
	}
}

func TestProjectionLastExpressionWinsForOneID(t *testing.T) {
	t.Parallel()

	// An edited column keeps its id and changes its path; the newer one is
	// the one the operator is looking at.
	projection := domain.Projection{}.WithExpressions([]domain.CustomExpression{
		{ID: "c1", Path: ".status.phase"},
		{ID: "c1", Path: ".spec.nodeName"},
	})

	got := projection.Expressions()
	if len(got) != 1 || got[0].Path != ".spec.nodeName" {
		t.Fatalf("expressions = %v, want the later path", got)
	}
}

// THE CACHE KEY IS THE POINT OF THIS ONE. A list read WITH expressions and
// the same list read without them differ only in fields nothing else looks
// at, so a shared key would serve one caller the other's answer and every
// JSONPath cell would be empty — or full — depending on which arrived first.
func TestProjectionStringSeparatesReadsThatDifferOnlyByExpression(t *testing.T) {
	t.Parallel()

	plain := domain.NewProjection([]string{"team"})
	withOne := plain.WithExpressions([]domain.CustomExpression{{ID: "c1", Path: ".status.phase"}})
	withOther := plain.WithExpressions([]domain.CustomExpression{{ID: "c1", Path: ".spec.nodeName"}})

	if plain.String() == withOne.String() {
		t.Error("a projection with an expression renders the same key as one without")
	}
	if withOne.String() == withOther.String() {
		t.Error("two different expressions render the same key")
	}
	// The annotation keys still travel, so a column set that differs only in
	// its annotations is still a different read.
	if !strings.Contains(withOne.String(), "team") {
		t.Errorf("key = %q, want the annotation keys kept", withOne.String())
	}
}

func TestProjectionNeedsWholeObjectOnlyForExpressions(t *testing.T) {
	t.Parallel()

	// Labels and annotations are on every row of every read PodSteer makes,
	// so asking for them changes nothing about how the list is fetched.
	if domain.NewProjection([]string{"team"}).NeedsWholeObject() {
		t.Error("an annotation column asked for a whole object")
	}
	withExpression := domain.Projection{}.WithExpressions([]domain.CustomExpression{{ID: "c1", Path: ".status.phase"}})
	if !withExpression.NeedsWholeObject() {
		t.Error("an expression did not ask for a whole object")
	}
}
