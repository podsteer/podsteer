package domain

import (
	"slices"
	"strings"
)

// LastAppliedConfigurationAnnotation is the key `kubectl apply` writes the
// whole previous manifest under. It is the one annotation no list may ever
// carry — see NewProjection.
const LastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// CustomExpression is one operator-written JSONPath column: the id the
// interface knows it by, and the path to read.
//
// THE ID TRAVELS BECAUSE THE PATH IS NOT A KEY. Two columns can read the same
// path, a path can be edited while a list is open, and a cell has to find its
// own value in the map that comes back — so the interface's own id is what
// the result is filed under, exactly as an annotation column files under its
// key.
type CustomExpression struct {
	// ID identifies the column. Never contains a comma, so a projection can
	// render itself as a cache key.
	ID string
	// Path is the JSONPath, as the operator typed it — `.status.phase`,
	// `{.spec.replicas}`, `.status.loadBalancer.ingress[0].ip`. The syntax
	// itself is the adapter's business; what is checked here is the shape
	// that PodSteer's own plumbing depends on.
	Path string
}

// Projection names the per-object metadata a list carries beyond what its
// own columns need: the annotation keys an operator has put on a custom
// column of that kind, and the JSONPath expressions they wrote.
//
// LABELS NEED NO PROJECTION AND ANNOTATIONS DO. A label is a short selector
// value, bounded by the API to 63 characters, and every list row ships all
// of them. An annotation is arbitrary text, and on a real cluster the map is
// dominated by kubectl's last-applied-configuration — a copy of the whole
// manifest, tens of kilobytes on a Deployment — re-sent on every refresh of
// every row. So a list carries only the keys somebody asked for, and this
// value is how they ask.
//
// The zero value carries nothing, which is what every caller that is not a
// list view passes: the assessment, the sampler and the dependency map read
// the same lists and want no annotations at all.
type Projection struct {
	annotationKeys []string
	expressions    []CustomExpression
}

// WithExpressions returns a projection that also evaluates these JSONPath
// columns.
//
// A SEPARATE CONSTRUCTOR RATHER THAN A SECOND ARGUMENT to NewProjection,
// because almost every caller in the application has no expressions and never
// will: the assessment, the sampler and the dependency map read the same
// lists and want neither annotations nor expressions. Only a list VIEW has
// them, and it is the one caller that says so.
//
// Expressions are trimmed, dropped when either half is empty, and de-duplicated
// by id — the last one wins, which is what an edited column means. The id may
// not contain a comma for the same reason an annotation key may not: String()
// below is a cache key.
func (p Projection) WithExpressions(expressions []CustomExpression) Projection {
	kept := make([]CustomExpression, 0, len(expressions))
	seen := make(map[string]int, len(expressions))

	for _, expression := range expressions {
		id := strings.TrimSpace(expression.ID)
		path := strings.TrimSpace(expression.Path)
		if id == "" || path == "" || strings.ContainsAny(id, ",\t\n\r ") {
			continue
		}
		if at, already := seen[id]; already {
			kept[at] = CustomExpression{ID: id, Path: path}
			continue
		}
		seen[id] = len(kept)
		kept = append(kept, CustomExpression{ID: id, Path: path})
	}

	slices.SortFunc(kept, func(a, b CustomExpression) int { return strings.Compare(a.ID, b.ID) })

	next := Projection{annotationKeys: p.annotationKeys}
	if len(kept) > 0 {
		next.expressions = kept
	}
	return next
}

// Expressions returns a copy of the JSONPath columns, sorted by id.
func (p Projection) Expressions() []CustomExpression { return slices.Clone(p.expressions) }

// NeedsWholeObject reports that this projection cannot be served from a
// stripped or metadata-only read.
//
// THE ONE QUESTION THE LIST PATHS ASK IT. A label or an annotation is on
// every row of every read PodSteer makes; a JSONPath expression may name
// anything, including the fields the watch store strips before it keeps a pod
// and the spec a server-printed table never carries at all. So a list with
// expressions goes to the network for whole objects, and one without keeps
// the cheap read it always made — see ListPods and ListTable.
func (p Projection) NeedsWholeObject() bool { return len(p.expressions) > 0 }

// NewProjection builds a projection over the given annotation keys.
//
// Keys are trimmed, de-duplicated and sorted, so two projections naming the
// same keys in a different order are the same projection — the adapter keys
// its read cache on String(), and a list read for one column set must be
// able to serve another that merely listed them differently. A blank key, or
// one containing whitespace or a comma, names nothing an annotation can be
// called and is dropped.
//
// THE LAST-APPLIED MANIFEST IS REFUSED, not merely discouraged. Beyond its
// size, the watch store strips it from every object it holds (see
// stripPod in the k8s adapter), so a column showing it would read blank on
// a cluster the watch is serving and the full manifest on one it is not —
// two answers for one key, decided by something the operator cannot see.
func NewProjection(annotationKeys []string) Projection {
	kept := make([]string, 0, len(annotationKeys))
	for _, key := range annotationKeys {
		key = strings.TrimSpace(key)
		if key == "" || key == LastAppliedConfigurationAnnotation {
			continue
		}
		if strings.ContainsAny(key, ", \t\n\r") {
			continue
		}
		kept = append(kept, key)
	}
	slices.Sort(kept)
	kept = slices.Compact(kept)
	if len(kept) == 0 {
		return Projection{}
	}
	return Projection{annotationKeys: kept}
}

// AnnotationKeys returns a copy of the keys, sorted.
func (p Projection) AnnotationKeys() []string { return slices.Clone(p.annotationKeys) }

// IsEmpty reports whether the projection asks for nothing.
func (p Projection) IsEmpty() bool { return len(p.annotationKeys) == 0 && len(p.expressions) == 0 }

// String renders the projection canonically — sorted keys, comma-separated,
// empty for the zero value — so it can stand as one component of a cache
// key. No valid annotation key contains a comma, and NewProjection drops any
// that does, which is what makes the rendering unambiguous.
func (p Projection) String() string {
	if len(p.expressions) == 0 {
		return strings.Join(p.annotationKeys, ",")
	}

	// THE EXPRESSIONS ARE PART OF THE KEY, and leaving them out would be the
	// worst kind of cache bug: a list read WITH expressions and the same list
	// read without them differ only in fields nothing else looks at, so the
	// second caller would be served the first's answer and every JSONPath
	// cell would be empty — or full, depending on which arrived first.
	parts := make([]string, 0, len(p.expressions))
	for _, expression := range p.expressions {
		parts = append(parts, expression.ID+"="+expression.Path)
	}
	return strings.Join(p.annotationKeys, ",") + "|" + strings.Join(parts, ",")
}

// Annotations returns the projected subset of all: the requested keys that
// are present, verbatim, and nothing else.
//
// Nil — never an empty map — when nothing was asked for or nothing matched.
// The mappers clone what they are given, and a nil here is what keeps an
// object read from the watch store and the same object read from the
// network mapping to values that are equal, not merely equivalent.
func (p Projection) Annotations(all map[string]string) map[string]string {
	if len(p.annotationKeys) == 0 || len(all) == 0 {
		return nil
	}

	var kept map[string]string
	for _, key := range p.annotationKeys {
		value, present := all[key]
		if !present {
			continue
		}
		if kept == nil {
			kept = make(map[string]string, len(p.annotationKeys))
		}
		kept[key] = value
	}
	return kept
}
