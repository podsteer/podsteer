package domain

import (
	"maps"
	"slices"
)

// ResourceTable is a generic tabular projection of a set of objects.
//
// It exists so PodSteer can browse kinds it has no purpose-built model for —
// every CRD in the cluster, and the long tail of built-in kinds where a bespoke
// entity would earn nothing. Kubernetes itself can print any resource as a
// table (the same mechanism kubectl uses for its output), so the columns come
// from the API server rather than from a list PodSteer would have to maintain
// and keep in step with every new controller anyone installs.
//
// Kinds that DO have a purpose-built model — pods, nodes, workloads — do not
// go through here. They get columns PodSteer chose, values it derived, and
// cross-links a generic projection cannot express.
type ResourceTable struct {
	kind    ResourceKind
	columns []TableColumn
	rows    []TableRow
}

// TableColumn describes one column of a generic table.
type TableColumn struct {
	// Name is the column heading, e.g. "Ready".
	Name string
	// Type is the cell data type as the API server reports it: "string",
	// "integer", "number" or "date". The UI uses it to align and sort
	// correctly — right-aligning numbers, sorting dates chronologically
	// rather than lexically.
	Type string
	// Priority is the API server's own hint at importance. Zero is the
	// standard set; anything higher is what `kubectl get -o wide` adds, which
	// is why those columns start hidden.
	Priority int32
	// Description explains the column, for a tooltip.
	Description string
}

// IsWide reports whether the column belongs to the extended set.
func (c TableColumn) IsWide() bool { return c.Priority > 0 }

// TableRow is one object rendered as cells.
type TableRow struct {
	// Name is the object's name, lifted out of the cells so the UI can
	// identify and link the row without guessing which column holds it.
	Name string
	// Namespace is empty for cluster-scoped objects.
	Namespace NamespaceName
	// Cells are the rendered values, positionally matching the columns.
	Cells []string
	// Labels are the object's labels, read from the metadata the server
	// attaches to each row — never from a second request per object.
	Labels map[string]string
	// Annotations are the projected subset of the object's annotations, from
	// the same row metadata. See Projection for why it is a subset.
	Annotations map[string]string
}

// NewResourceTable assembles a table, guaranteeing every row has exactly one
// cell per column.
//
// Rows shorter than the column set are padded and longer rows are truncated,
// rather than being rejected. A ragged table from an unusual CRD printer
// should degrade into a slightly empty row, never into an error that hides
// every other object of that kind.
func NewResourceTable(kind ResourceKind, columns []TableColumn, rows []TableRow) ResourceTable {
	normalised := make([]TableRow, 0, len(rows))
	for _, row := range rows {
		cells := row.Cells
		switch {
		case len(cells) < len(columns):
			padded := make([]string, len(columns))
			copy(padded, cells)
			cells = padded
		case len(cells) > len(columns):
			cells = cells[:len(columns)]
		}
		normalised = append(normalised, TableRow{
			Name:        row.Name,
			Namespace:   row.Namespace,
			Cells:       cells,
			Labels:      maps.Clone(row.Labels),
			Annotations: maps.Clone(row.Annotations),
		})
	}

	return ResourceTable{
		kind:    kind,
		columns: slices.Clone(columns),
		rows:    normalised,
	}
}

// Kind returns the kind the table describes.
func (t ResourceTable) Kind() ResourceKind { return t.kind }

// Columns returns a copy of the column definitions.
func (t ResourceTable) Columns() []TableColumn { return slices.Clone(t.columns) }

// Rows returns a copy of the rows.
func (t ResourceTable) Rows() []TableRow { return slices.Clone(t.rows) }

// Len returns the number of rows.
func (t ResourceTable) Len() int { return len(t.rows) }
