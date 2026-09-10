package domain

import (
	"reflect"
	"testing"
)

var widgetKind = ResourceKind{
	Group: "acme.io", Version: "v1", Resource: "widgets",
	Kind: "Widget", Namespaced: true, Title: "Widgets",
}

// fullRow is a row with EVERY exported field set to something non-zero.
//
// Kept as a literal rather than built by reflection so that removing a field
// from TableRow breaks the compile here, and adding one is caught by the
// zero-field check below rather than passing silently.
func fullRow() TableRow {
	return TableRow{
		Name:        "app-config",
		Namespace:   NamespaceName("platform"),
		Cells:       []string{"app-config"},
		Labels:      map[string]string{"app": "web"},
		Annotations: map[string]string{"team": "payments"},
		Custom:      map[string]string{"tier": "gold"},
	}
}

// assertNoZeroFields names every exported field left at its zero value.
func assertNoZeroFields(t *testing.T, subject string, value reflect.Value) {
	t.Helper()

	typ := value.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		if value.Field(i).IsZero() {
			t.Errorf("%s: %s.%s is zero", subject, typ.Name(), field.Name)
		}
	}
}

// TestNewResourceTableCarriesEveryFieldOfARow is a drift guard over a
// constructor that rebuilds its input field by field.
//
// THE BUG IT EXISTS FOR, which is not hypothetical. NewResourceTable
// normalises each row's cells and reassembles the row around them, naming
// every field it keeps. `Custom` — the operator's own JSONPath columns — was
// added to TableRow and not added here, so it was dropped on the threshold of
// the domain: the column appeared on every generic table, correctly named,
// with every cell in it empty. Worse, the read had ALREADY paid for it, since
// an expression makes the list fetch whole objects rather than metadata (see
// Projection.NeedsWholeObject). Nothing failed, because a row with one fewer
// field populated is still a valid row.
//
// The check is in two halves on purpose. The fixture assertion fails first if
// a new field is added to TableRow and not set here, which sends the author
// to this test; the round-trip assertion then fails if the constructor does
// not carry it, which sends them to the constructor. Neither can be satisfied
// by editing only one of the two places.
func TestNewResourceTableCarriesEveryFieldOfARow(t *testing.T) {
	t.Parallel()

	assertNoZeroFields(t, "the fixture", reflect.ValueOf(fullRow()))

	table := NewResourceTable(widgetKind, []TableColumn{{Name: "Name", Type: "string"}}, []TableRow{fullRow()})

	rows := table.Rows()
	if len(rows) != 1 {
		t.Fatalf("Rows() = %d, want 1", len(rows))
	}
	assertNoZeroFields(t, "the row that came back", reflect.ValueOf(rows[0]))

	if got := rows[0].Custom["tier"]; got != "gold" {
		t.Errorf("Custom[tier] = %q, want gold", got)
	}
	if got := rows[0].Labels["app"]; got != "web" {
		t.Errorf("Labels[app] = %q, want web", got)
	}
	if got := rows[0].Annotations["team"]; got != "payments" {
		t.Errorf("Annotations[team] = %q, want payments", got)
	}
}

// TestNewResourceTableCopiesTheMapsItIsGiven pins the reason those fields go
// through maps.Clone rather than being assigned.
//
// A table is handed to the bridge and may be held while the caller reads the
// next one. A shared map is how one caller's mutation becomes another's row.
func TestNewResourceTableCopiesTheMapsItIsGiven(t *testing.T) {
	t.Parallel()

	row := fullRow()
	table := NewResourceTable(widgetKind, []TableColumn{{Name: "Name", Type: "string"}}, []TableRow{row})

	row.Labels["app"] = "mutated"
	row.Annotations["team"] = "mutated"
	row.Custom["tier"] = "mutated"

	got := table.Rows()[0]
	for name, value := range map[string]string{
		"Labels[app]":       got.Labels["app"],
		"Annotations[team]": got.Annotations["team"],
		"Custom[tier]":      got.Custom["tier"],
	} {
		if value == "mutated" {
			t.Errorf("%s followed the caller's mutation — the map was shared, not copied", name)
		}
	}
}

// TestNewResourceTableIsCompleteUntilSaidOtherwise pins the default.
//
// Every existing caller builds a complete table and says nothing, so silence
// has to mean complete — and only the one read that imposes a limit has to
// know about truncation at all. See WithTruncation.
func TestNewResourceTableIsCompleteUntilSaidOtherwise(t *testing.T) {
	t.Parallel()

	table := NewResourceTable(widgetKind, []TableColumn{{Name: "Name", Type: "string"}}, []TableRow{fullRow()})

	if table.Truncated() {
		t.Error("a table nobody capped reports itself truncated")
	}
	if table.Cap() != 0 {
		t.Errorf("Cap() = %d, want 0 when nothing stopped the read", table.Cap())
	}

	capped := table.WithTruncation(1000)
	if !capped.Truncated() || capped.Cap() != 1000 {
		t.Errorf("WithTruncation(1000) = truncated %v cap %d, want true and 1000", capped.Truncated(), capped.Cap())
	}
	if table.Truncated() {
		t.Error("WithTruncation mutated the table it was called on rather than returning a copy")
	}
}

// TestWithExpressionsCarriesEveryFieldOfAnExpression is the same guard over
// the only other constructor in this package that REBUILDS an instance of its
// own input type field by field.
//
// WithExpressions trims and de-duplicates, and both paths write a fresh
// CustomExpression naming each field. That is the shape that lost
// TableRow.Custom — a field added to the struct and not to the literal, which
// nothing catches because a struct with one fewer field populated is still a
// valid struct. Two fields today; this is here so a third cannot go missing.
func TestWithExpressionsCarriesEveryFieldOfAnExpression(t *testing.T) {
	t.Parallel()

	expression := CustomExpression{ID: "tier", Path: "{.metadata.labels.tier}"}
	assertNoZeroFields(t, "the fixture", reflect.ValueOf(expression))

	// Twice, so the de-duplication path — which writes its own literal — is
	// the one that produces the surviving entry.
	kept := Projection{}.WithExpressions([]CustomExpression{expression, expression}).Expressions()

	if len(kept) != 1 {
		t.Fatalf("Expressions() = %d, want 1 after de-duplication", len(kept))
	}
	assertNoZeroFields(t, "the expression that came back", reflect.ValueOf(kept[0]))
}
