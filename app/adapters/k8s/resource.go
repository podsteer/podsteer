package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredv1 "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/podsteer/podsteer/app/domain"
)

// tableMediaType asks the API server to render objects as a table.
//
// This is the same mechanism kubectl uses for its output: the server decides
// the columns and formats the cells. PodSteer leans on it for every kind it has
// no purpose-built model for, which is what makes a freshly installed
// operator's CRDs browsable without anyone writing code for them — and what
// keeps the columns right when a CRD changes its printer columns.
const tableMediaType = "application/json;as=Table;v=v1;g=meta.k8s.io"

// tableListLimit caps a single generic list. Some CRDs hold enormous
// collections, and a browser that fetches all of them stalls the window.
const tableListLimit = 1000

// ListTable returns objects of the given kind rendered as a table.
//
// projection names the annotation keys each row carries; labels are always
// carried. Both are read from the metadata the server already attaches to
// every row (see includeObject below), so a custom column costs no request
// beyond the one list this always made.
func (a *Adapter) ListTable(ctx context.Context, id domain.ClusterID, kind domain.ResourceKind, namespace domain.NamespaceName, projection domain.Projection) (domain.ResourceTable, error) {
	op := fmt.Sprintf("listing %s in %q of %q", kind.Resource, namespace, id)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return domain.ResourceTable{}, err
	}

	restClient := set.discovery.RESTClient()
	if restClient == nil {
		return domain.ResourceTable{}, fmt.Errorf("%s: no REST client available", op)
	}

	// includeObject=Metadata makes the server attach each row's object
	// metadata. Without it a row is only rendered cells, and PodSteer would
	// have to guess which column holds the name in order to link the row —
	// a guess that breaks on any CRD whose printer puts the name elsewhere.
	// The same attachment carries the labels and annotations, which is what
	// lets a custom column on a CRD read them without a GET per row.
	body, err := restClient.Get().
		AbsPath(resourcePath(kind, namespace, "")).
		SetHeader("Accept", tableMediaType).
		Param("includeObject", "Metadata").
		Param("limit", fmt.Sprint(tableListLimit)).
		DoRaw(ctx)
	if err != nil {
		return domain.ResourceTable{}, classify(op, err)
	}

	var table metav1.Table
	if err := json.Unmarshal(body, &table); err != nil {
		return domain.ResourceTable{}, fmt.Errorf("%s: decoding table: %w", op, err)
	}

	return mapTable(kind, &table, projection)
}

// GetManifest returns one object serialised as YAML.
func (a *Adapter) GetManifest(ctx context.Context, ref domain.ResourceRef, revealSecrets bool) (string, error) {
	op := fmt.Sprintf("reading manifest of %s in %q", ref, ref.ClusterID)

	set, err := a.factory.clientsFor(ref.ClusterID)
	if err != nil {
		return "", err
	}

	gvr := schema.GroupVersionResource{
		Group:    ref.Kind.Group,
		Version:  ref.Kind.Version,
		Resource: ref.Kind.Resource,
	}

	var client = set.dynamic.Resource(gvr)
	object, err := func() (any, error) {
		if ref.Kind.Namespaced {
			return client.Namespace(ref.Namespace.String()).Get(ctx, ref.Name, metav1.GetOptions{})
		}
		return client.Get(ctx, ref.Name, metav1.GetOptions{})
	}()
	if err != nil {
		return "", classify(op, err)
	}

	// A Secret's values are replaced BEFORE anything is serialised, so the
	// material never reaches the caller — let alone the frontend — unless it
	// was deliberately asked for. Masking after the fact, or in the UI, would
	// mean the credential had already crossed every boundary in between.
	if !revealSecrets {
		maskSecretData(object)
	}

	// Marshal through JSON: unstructured objects are map[string]any, and
	// sigs.k8s.io/yaml round-trips them through encoding/json so that the
	// field ordering and types match what the API server returned.
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("%s: encoding object: %w", op, err)
	}

	manifest, err := yaml.JSONToYAML(encoded)
	if err != nil {
		return "", fmt.Errorf("%s: converting to YAML: %w", op, err)
	}

	return string(manifest), nil
}

// resourcePath builds the API path for a kind, optionally naming one object.
//
// The core group lives under /api and everything else under /apis — a
// distinction Kubernetes has carried since v1 and the single most common
// source of hand-built path bugs.
func resourcePath(kind domain.ResourceKind, namespace domain.NamespaceName, name string) string {
	var builder strings.Builder

	if kind.Group == "" {
		builder.WriteString("/api/")
		builder.WriteString(kind.Version)
	} else {
		builder.WriteString("/apis/")
		builder.WriteString(kind.Group)
		builder.WriteString("/")
		builder.WriteString(kind.Version)
	}

	if kind.Namespaced && !namespace.IsAll() {
		builder.WriteString("/namespaces/")
		builder.WriteString(namespace.String())
	}

	builder.WriteString("/")
	builder.WriteString(kind.Resource)

	if name != "" {
		builder.WriteString("/")
		builder.WriteString(name)
	}

	return builder.String()
}

// mapTable translates a server-printed table into the domain projection.
func mapTable(kind domain.ResourceKind, table *metav1.Table, projection domain.Projection) (domain.ResourceTable, error) {
	columns := make([]domain.TableColumn, 0, len(table.ColumnDefinitions))
	for _, definition := range table.ColumnDefinitions {
		columns = append(columns, domain.TableColumn{
			Name:        definition.Name,
			Type:        definition.Type,
			Priority:    definition.Priority,
			Description: definition.Description,
		})
	}

	rows := make([]domain.TableRow, 0, len(table.Rows))
	for i := range table.Rows {
		row := &table.Rows[i]

		cells := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			cells = append(cells, renderCell(cell))
		}

		metadata := rowMetadata(row, projection)
		rows = append(rows, domain.TableRow{
			Name:        metadata.name,
			Namespace:   metadata.namespace,
			Cells:       cells,
			Labels:      metadata.labels,
			Annotations: metadata.annotations,
		})
	}

	return domain.NewResourceTable(kind, columns, rows), nil
}

// tableRowMetadata is what a row's attached PartialObjectMetadata yields.
type tableRowMetadata struct {
	name        string
	namespace   domain.NamespaceName
	labels      map[string]string
	annotations map[string]string
}

// rowMetadata extracts a row's identity, labels and projected annotations
// from its attached metadata.
//
// Falls back to the zero value rather than failing: a row whose object could
// not be decoded is still worth displaying, it just cannot be clicked through
// to a detail view and its custom columns read blank.
func rowMetadata(row *metav1.TableRow, projection domain.Projection) tableRowMetadata {
	if len(row.Object.Raw) == 0 {
		return tableRowMetadata{namespace: domain.NamespaceAll}
	}

	var partial metav1.PartialObjectMetadata
	if err := json.Unmarshal(row.Object.Raw, &partial); err != nil {
		return tableRowMetadata{namespace: domain.NamespaceAll}
	}

	namespace, err := domain.NewNamespaceName(partial.Namespace)
	if err != nil {
		namespace = domain.NamespaceAll
	}

	return tableRowMetadata{
		name:        partial.Name,
		namespace:   namespace,
		labels:      partial.Labels,
		annotations: projection.Annotations(partial.Annotations),
	}
}

// renderCell converts a table cell to its display string.
//
// Cells arrive as arbitrary JSON values because a printer column can be any
// type. A nil cell renders as an em dash rather than "<nil>", which is what
// fmt would otherwise produce and what would then appear in the UI.
func renderCell(cell any) string {
	switch value := cell.(type) {
	case nil:
		return "—"
	case string:
		return value
	case float64:
		// JSON numbers decode as float64. Integers are the overwhelmingly
		// common case in printer columns (replica counts, ports), and
		// rendering them as "3" rather than "3.000000" matters.
		if value == float64(int64(value)) {
			return fmt.Sprintf("%d", int64(value))
		}
		return fmt.Sprintf("%g", value)
	case bool:
		return fmt.Sprintf("%t", value)
	default:
		return fmt.Sprint(value)
	}
}

// RevealSecretKey returns one decoded value from one Secret.
//
// The typed client is used rather than the dynamic one deliberately:
// corev1.Secret.Data is []byte already base64-decoded by client-go, so
// nothing here does its own decoding and there is no encoded copy of the
// value in this process to leak into a log line or an error string.
//
// ONE KEY LEAVES THIS FUNCTION. The Secret is fetched whole because the API
// offers no narrower read, but everything except the requested key is
// discarded here, at the boundary, rather than travelling up through the
// application and across the Wails bridge where each layer is another place a
// value can be logged, cached or serialised into a crash report.
func (a *Adapter) RevealSecretKey(ctx context.Context, id domain.ClusterID, namespace domain.NamespaceName, name, key string) (string, error) {
	op := fmt.Sprintf("reading key %q of secret %q in %q", key, name, namespace)

	set, err := a.factory.clientsFor(id)
	if err != nil {
		return "", err
	}

	secret, err := set.typed.CoreV1().Secrets(namespace.String()).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", classify(op, err)
	}

	value, ok := secret.Data[key]
	if !ok {
		// A key named by a pod that the Secret does not have. The pod is
		// misconfigured — or the Secret changed under it — and saying so is
		// more useful than an empty string that reads like an empty value.
		return "", fmt.Errorf("%s: %w", op, domain.ErrSecretKeyNotFound)
	}

	return string(value), nil
}

// maskSecretData replaces a Secret's values with their decoded size.
//
// BASE64 IS NOT MASKING, and treating it as though it were is the single most
// exploitable habit in this category of tool: a screenshot of a pane showing
// `cGFzc3dvcmQ=` has leaked the password to anybody who can type `base64 -d`.
// So the encoded form is not shown either — what is left is the shape of the
// value and nothing of its content, which is exactly what
// `kubectl describe secret` prints and for the same reason.
//
// Applied to `data` and `stringData` on core/v1 Secrets only. Everything
// else — a ConfigMap, a CRD holding something sensitive by convention — is
// returned untouched, because guessing at which fields of an arbitrary kind
// are secret would mask things arbitrarily and still miss the ones that
// matter.
func maskSecretData(object any) {
	unstructured, ok := object.(*unstructuredv1.Unstructured)
	if !ok {
		return
	}
	if unstructured.GetKind() != "Secret" || unstructured.GetAPIVersion() != "v1" {
		return
	}

	for _, field := range []string{"data", "stringData"} {
		values, found, err := unstructuredv1.NestedMap(unstructured.Object, field)
		if err != nil || !found {
			continue
		}

		for key, value := range values {
			encoded, isString := value.(string)
			if !isString {
				continue
			}

			// The decoded length, so the placeholder says something true
			// about the value. A key that fails to decode is reported as
			// present rather than guessed at.
			size := "unreadable"
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
				size = fmt.Sprintf("%d bytes", len(decoded))
			} else if field == "stringData" {
				size = fmt.Sprintf("%d bytes", len(encoded))
			}
			values[key] = fmt.Sprintf("<hidden, %s>", size)
		}

		// Errors here would mean the object is not shaped like a Secret at
		// all, in which case there is nothing to mask and nothing to fix.
		_ = unstructuredv1.SetNestedMap(unstructured.Object, values, field)
	}
}
