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
	// truncated says the read stopped at its cap with objects left unread,
	// so the rows are a PREFIX of the kind rather than all of it. See
	// WithTruncation for why this is on the table rather than left implicit
	// in a row count.
	truncated bool
	// cap is the limit that stopped it, for a sentence that can name it.
	cap int
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
	// Custom holds the operator's own JSONPath columns, keyed by the
	// interface's column id and already rendered as text. Nil unless the
	// projection carried expressions — see Projection.NeedsWholeObject for
	// what asking for one changes about the read.
	Custom map[string]string
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
			// CARRIED, which it was not. This constructor rebuilds every row
			// field by field, and Custom was added to TableRow without being
			// added here — so an operator's JSONPath columns arrived from the
			// adapter and were dropped on the threshold of the domain. The
			// column appeared, correctly named, and every cell in it was
			// empty on every generic table, while the read had already paid
			// for the whole object to compute them (Projection.NeedsWholeObject).
			Custom: maps.Clone(row.Custom),
		})
	}

	return ResourceTable{
		kind:    kind,
		columns: slices.Clone(columns),
		rows:    normalised,
	}
}

// WithTruncation marks the table as a prefix, stopped by cap.
//
// A COPY WITH A FLAG RATHER THAN A CONSTRUCTOR ARGUMENT, so that every
// existing caller keeps reading as it did: a table is complete unless
// something says otherwise, and only the one read that imposes a limit has to
// know about this.
//
// WHY IT HAS TO BE SAID AT ALL. A generic list is capped so a CRD holding a
// hundred thousand objects cannot stall the window, and the rows that come
// back are indistinguishable from a complete answer — same columns, same
// shape, a plausible count. Every question the interface then answers is
// wrong in the same silent direction: the search finds nothing because the
// match was past the cut, the sort names the wrong newest, the count in the
// navigator is a floor presented as a total. Only the table can know, so only
// the table can say.
func (t ResourceTable) WithTruncation(cap int) ResourceTable {
	t.truncated = true
	t.cap = cap
	return t
}

// Kind returns the kind the table describes.
func (t ResourceTable) Kind() ResourceKind { return t.kind }

// Truncated reports whether the read stopped at its cap with objects unread.
func (t ResourceTable) Truncated() bool { return t.truncated }

// Cap returns the limit that stopped the read, or zero when nothing did.
func (t ResourceTable) Cap() int { return t.cap }

// Columns returns a copy of the column definitions.
func (t ResourceTable) Columns() []TableColumn { return slices.Clone(t.columns) }

// Rows returns a copy of the rows.
func (t ResourceTable) Rows() []TableRow { return slices.Clone(t.rows) }

// Len returns the number of rows.
func (t ResourceTable) Len() int { return len(t.rows) }
