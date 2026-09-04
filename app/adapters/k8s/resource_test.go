package k8s

import (
	"encoding/json"
	"reflect"
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
