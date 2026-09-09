package wails

import "github.com/podsteer/podsteer/app/domain"

// CustomExpression is one operator-written JSONPath column, as the interface
// sends it: the column's own id, and the path to read.
//
// A STRUCT RATHER THAN TWO PARALLEL SLICES. Every list call already takes the
// annotation keys as a bare []string, and a second bare slice beside it —
// ids here, paths there, correct only while their lengths agree — is the
// shape that goes wrong silently the first time one is filtered.
type CustomExpression struct {
	// ID is the column id the interface knows this column by. It comes back
	// as the key of each row's `custom` map.
	ID string `json:"id"`
	// Path is the JSONPath, as typed: `.status.phase`, `{.spec.replicas}`.
	Path string `json:"path"`
}

// projectionFor builds the projection one list read is made under.
//
// ONE PLACE, so the six list endpoints cannot disagree about what an empty
// expression list means or how a projection is assembled. Annotation keys and
// expressions travel together because they are the same feature seen from two
// sides: a custom column reads either metadata or an expression, and the read
// has to carry whichever it is.
func projectionFor(annotationKeys []string, expressions []CustomExpression) domain.Projection {
	projection := domain.NewProjection(annotationKeys)
	if len(expressions) == 0 {
		return projection
	}

	converted := make([]domain.CustomExpression, 0, len(expressions))
	for _, expression := range expressions {
		converted = append(converted, domain.CustomExpression{ID: expression.ID, Path: expression.Path})
	}
	return projection.WithExpressions(converted)
}
