package k8s

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/podsteer/podsteer/app/domain"
)

// tableRowWithMetadata builds one server-printed row carrying the
// PartialObjectMetadata the API server attaches under includeObject=Metadata.
func tableRowWithMetadata(t *testing.T, meta metav1.ObjectMeta, cells ...any) metav1.TableRow {
	t.Helper()

	raw, err := json.Marshal(&metav1.PartialObjectMetadata{
		TypeMeta:   metav1.TypeMeta{Kind: "PartialObjectMetadata", APIVersion: "meta.k8s.io/v1"},
		ObjectMeta: meta,
	})
	if err != nil {
		t.Fatalf("marshalling row metadata: %v", err)
	}
	return metav1.TableRow{Cells: cells, Object: runtime.RawExtension{Raw: raw}}
}

var configMapKind = domain.ResourceKind{
	Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true, Title: "Config Maps",
}

func TestMapTableReadsLabelsAndProjectedAnnotationsFromRowMetadata(t *testing.T) {
	t.Parallel()

	// THE ROW'S OWN METADATA IS THE ONLY SOURCE. The table request already
	// attaches it to every row, so a custom column on a CRD costs no request
	// beyond the list — and this is what pins that nothing else is needed.
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string"},
			{Name: "Data", Type: "integer"},
		},
		Rows: []metav1.TableRow{
			tableRowWithMetadata(t, metav1.ObjectMeta{
				Name:      "app-config",
				Namespace: "platform",
				Labels:    map[string]string{"app": "web"},
				Annotations: map[string]string{
					"team":                             "payments",
					"owner":                            "alice",
					corev1.LastAppliedConfigAnnotation: `{"whole":"manifest"}`,
				},
			}, "app-config", float64(3)),
		},
	}

	mapped, err := mapTable(configMapKind, table, domain.NewProjection([]string{"team"}))
	if err != nil {
		t.Fatalf("mapTable() error = %v", err)
	}
	rows := mapped.Rows()
	if len(rows) != 1 {
		t.Fatalf("Rows() = %d, want 1", len(rows))
	}

	row := rows[0]
	if row.Name != "app-config" || row.Namespace != "platform" {
		t.Errorf("identity = %q/%q, want platform/app-config", row.Namespace, row.Name)
	}
	if got, want := row.Cells, []string{"app-config", "3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Cells = %v, want %v", got, want)
	}
	if got, want := row.Labels, map[string]string{"app": "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Labels = %v, want %v", got, want)
	}
	if got, want := row.Annotations, map[string]string{"team": "payments"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Annotations = %v, want only the projected key %v", got, want)
	}
}

func TestMapTableWithoutAProjectionCarriesNoAnnotations(t *testing.T) {
	t.Parallel()

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}},
		Rows: []metav1.TableRow{
			tableRowWithMetadata(t, metav1.ObjectMeta{
				Name:        "app-config",
				Namespace:   "platform",
				Labels:      map[string]string{"app": "web"},
				Annotations: map[string]string{"team": "payments"},
			}, "app-config"),
		},
	}

	mapped, err := mapTable(configMapKind, table, domain.Projection{})
	if err != nil {
		t.Fatalf("mapTable() error = %v", err)
	}

	row := mapped.Rows()[0]
	if row.Annotations != nil {
		t.Errorf("Annotations = %v, want nil when nothing was asked for", row.Annotations)
	}
	// Labels ride along regardless: they are not what the projection is
	// about.
	if got, want := row.Labels, map[string]string{"app": "web"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Labels = %v, want %v", got, want)
	}
}

func TestMapTableSurvivesARowWithoutMetadata(t *testing.T) {
	t.Parallel()

	// A row the server printed without an object — or with one that will
	// not decode — is still a row. It cannot be opened and its custom
	// columns read blank, but it must not take the rest of the table down.
	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}},
		Rows: []metav1.TableRow{
			{Cells: []any{"orphan"}},
			{Cells: []any{"garbled"}, Object: runtime.RawExtension{Raw: []byte("not json")}},
		},
	}

	mapped, err := mapTable(configMapKind, table, domain.NewProjection([]string{"team"}))
	if err != nil {
		t.Fatalf("mapTable() error = %v", err)
	}
	if mapped.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", mapped.Len())
	}
	for _, row := range mapped.Rows() {
		if row.Name != "" || row.Labels != nil || row.Annotations != nil {
			t.Errorf("row without metadata = %+v, want no identity and no metadata", row)
		}
	}
}

// TestMapTableSaysWhenTheReadStoppedAtItsCap is the sentence a capped list
// has to carry.
//
// THE BUG. A generic list is capped at tableListLimit so a CRD holding a
// hundred thousand objects cannot stall the window, and what came back was
// indistinguishable from a complete answer — same columns, same shape, a
// plausible count. Every question the interface then answered was wrong in
// the same silent direction: the search found nothing because the match was
// past the cut, the sort named the wrong newest, and the navigator's count
// was a floor presented as a total.
func TestMapTableSaysWhenTheReadStoppedAtItsCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		continueToken string
		rows          int
		want          bool
	}{
		{
			name:          "the server says there is more",
			continueToken: "eyJ2IjoibWV0YS5rOHMuaW8vdjEi",
			rows:          1,
			want:          true,
		},
		{
			name: "the collection ended",
			rows: 1,
			want: false,
		},
		{
			// The belt-and-braces half, and the only reason this is
			// observable against a client that honours neither limit nor
			// continue: it hands back everything in one page. The same guard
			// the deprecation scan carries.
			name: "more rows came back than were asked for",
			rows: tableListLimit + 1,
			want: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			rows := make([]metav1.TableRow, 0, test.rows)
			for i := range test.rows {
				rows = append(rows, tableRowWithMetadata(t,
					metav1.ObjectMeta{Name: "cm-" + strconv.Itoa(i), Namespace: "platform"},
					"cm-"+strconv.Itoa(i)))
			}

			table := &metav1.Table{
				ListMeta:          metav1.ListMeta{Continue: test.continueToken},
				ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}},
				Rows:              rows,
			}

			mapped, err := mapTable(configMapKind, table, domain.Projection{})
			if err != nil {
				t.Fatalf("mapTable() error = %v", err)
			}

			if mapped.Truncated() != test.want {
				t.Fatalf("Truncated() = %v, want %v", mapped.Truncated(), test.want)
			}
			if test.want && mapped.Cap() != tableListLimit {
				t.Errorf("Cap() = %d, want %d — the sentence has to be able to name the limit",
					mapped.Cap(), tableListLimit)
			}
		})
	}
}

// TestMapTableCarriesCustomColumnsThroughTheConstructor pins a field that was
// being dropped on the threshold of the domain.
//
// NewResourceTable rebuilds every row field by field, and Custom was added to
// TableRow without being added there. So an operator's JSONPath column
// appeared, correctly named, with every cell in it empty on every generic
// table — while the read had already paid for the whole object to compute it.
func TestMapTableCarriesCustomColumnsThroughTheConstructor(t *testing.T) {
	t.Parallel()

	table := &metav1.Table{
		ColumnDefinitions: []metav1.TableColumnDefinition{{Name: "Name", Type: "string"}},
		Rows: []metav1.TableRow{
			tableRowWithMetadata(t, metav1.ObjectMeta{
				Name:      "app-config",
				Namespace: "platform",
				Labels:    map[string]string{"tier": "gold"},
			}, "app-config"),
		},
	}

	projection := domain.Projection{}.WithExpressions([]domain.CustomExpression{
		{ID: "tier", Path: "{.metadata.labels.tier}"},
	})

	mapped, err := mapTable(configMapKind, table, projection)
	if err != nil {
		t.Fatalf("mapTable() error = %v", err)
	}

	rows := mapped.Rows()
	if len(rows) != 1 {
		t.Fatalf("Rows() = %d, want 1", len(rows))
	}
	if got := rows[0].Custom["tier"]; got != "gold" {
		t.Fatalf("Custom[tier] = %q, want gold — the column would render empty on every row", got)
	}
}
