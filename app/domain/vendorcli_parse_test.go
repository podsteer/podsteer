package domain

import (
	"errors"
	"testing"
)

// The two output shapes the table has to cope with, written as the CLIs print
// them. Which row is which is not asserted — the point of the table is that
// the parser does not know — so each case finds the row whose shape it fits.

// listRow returns the id of a row declaring the given items key.
func listRow(t *testing.T, items string) string {
	t.Helper()

	clis, err := VendorCLIs()
	if err != nil {
		t.Fatalf("VendorCLIs() = %v", err)
	}
	for _, cli := range clis {
		row, err := vendorRow(cli.ID)
		if err != nil {
			t.Fatalf("vendorRow(%q) = %v", cli.ID, err)
		}
		if row.List.Items == items {
			return cli.ID
		}
	}
	t.Skipf("no shipped row reads an items key of %q", items)
	return ""
}

// An array of bare strings: the whole element is the name.
func TestAnArrayOfNamesIsRead(t *testing.T) {
	t.Parallel()

	id := listRow(t, "clusters")
	clusters, err := ParseVendorList(id, []byte(`{"clusters":["alpha","beta"]}`))
	if err != nil {
		t.Fatalf("ParseVendorList() = %v", err)
	}

	if len(clusters) != 2 || clusters[0].Name != "alpha" || clusters[1].Name != "beta" {
		t.Errorf("clusters = %+v, want alpha and beta", clusters)
	}
}

// An array of objects at the root, with extra fields the add command needs.
func TestAnArrayOfObjectsKeepsWhatTheAddCommandNeeds(t *testing.T) {
	t.Parallel()

	id := listRow(t, "")
	clusters, err := ParseVendorList(id, []byte(
		`[{"name":"alpha","resourceGroup":"rg-1","location":"westeurope"}]`))
	if err != nil {
		t.Fatalf("ParseVendorList() = %v", err)
	}

	if len(clusters) != 1 {
		t.Fatalf("clusters = %+v, want one", clusters)
	}
	if clusters[0].Name != "alpha" {
		t.Errorf("name = %q, want alpha", clusters[0].Name)
	}
	if got := clusters[0].Params["resourceGroup"]; got != "rg-1" {
		t.Errorf("resourceGroup = %q, want rg-1 — the add command cannot name the cluster without it", got)
	}
}

// AN EMPTY LIST IS AN ANSWER. An account with no clusters and a CLI that would
// not answer need opposite responses from an operator, so they must not arrive
// as the same empty slice — hence VendorListStatus, and hence this.
func TestAnEmptyListIsNotAFailure(t *testing.T) {
	t.Parallel()

	id := listRow(t, "clusters")
	clusters, err := ParseVendorList(id, []byte(`{"clusters":[]}`))
	if err != nil {
		t.Fatalf("ParseVendorList() = %v, want an empty list rather than an error", err)
	}
	if len(clusters) != 0 {
		t.Errorf("clusters = %+v, want none", clusters)
	}
}

// Output that is not the declared shape is refused whole. A list that quietly
// lost half its clusters is worse than an error: an operator would go looking
// for the missing one in the wrong place.
func TestOutputOfTheWrongShapeIsRefusedWhole(t *testing.T) {
	t.Parallel()

	id := listRow(t, "clusters")
	for _, output := range []string{
		`not json at all`,
		`{"clusters":"alpha"}`,
		`["alpha"]`,
		`{}`,
		`{"clusters":[{"unexpected":"shape"}]}`,
	} {
		if _, err := ParseVendorList(id, []byte(output)); !errors.Is(err, ErrVendorOutputUnreadable) {
			t.Errorf("ParseVendorList(%s) = %v, want ErrVendorOutputUnreadable", output, err)
		}
	}
}

// An object WITHOUT the declared key is not an empty list. Reading it as one
// would report an account as empty because a CLI changed its output.
func TestAnObjectMissingTheKeyIsNotAnEmptyAccount(t *testing.T) {
	t.Parallel()

	id := listRow(t, "clusters")
	if _, err := ParseVendorList(id, []byte(`{"somethingElse":[]}`)); !errors.Is(err, ErrVendorOutputUnreadable) {
		t.Errorf("error = %v, want ErrVendorOutputUnreadable rather than a claim of no clusters", err)
	}
}

// A field a row declares but the output omits is left empty rather than
// refused: the row may declare an optional one, and the plan refuses later if
// the add command actually needed it. That refusal is tested in
// TestAMissingFieldIsRefused.
func TestAnOptionalFieldMayBeAbsent(t *testing.T) {
	t.Parallel()

	id := listRow(t, "")
	clusters, err := ParseVendorList(id, []byte(`[{"name":"alpha","resourceGroup":"rg-1"}]`))
	if err != nil {
		t.Fatalf("ParseVendorList() = %v", err)
	}
	if clusters[0].Params["location"] != "" {
		t.Errorf("params = %+v, want no location invented", clusters[0].Params)
	}
}

// The heading picker reads the CLI's words to choose a sentence, and choosing
// wrong costs nothing: the CLI's own text is shown either way.
func TestTheDeclinedHeadingIsOnlyAHeading(t *testing.T) {
	t.Parallel()

	clis, _ := VendorCLIs()
	for _, cli := range clis {
		row, _ := vendorRow(cli.ID)
		if len(row.DeclinedHeadings) == 0 {
			continue
		}
		if !VendorDeclinedHeading(cli.ID, "something "+row.DeclinedHeadings[0]+" happened") {
			t.Errorf("%s did not recognise its own sign-in wording", cli.ID)
		}
		if VendorDeclinedHeading(cli.ID, "an unrelated failure") {
			t.Errorf("%s claimed a sign-in problem from unrelated words", cli.ID)
		}
	}
}
