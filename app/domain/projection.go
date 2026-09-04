package domain

import (
	"slices"
	"strings"
)

// LastAppliedConfigurationAnnotation is the key `kubectl apply` writes the
// whole previous manifest under. It is the one annotation no list may ever
// carry — see NewProjection.
const LastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// Projection names the per-object metadata a list carries beyond what its
// own columns need: the annotation keys an operator has put on a custom
// column of that kind.
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
}

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
func (p Projection) IsEmpty() bool { return len(p.annotationKeys) == 0 }

// String renders the projection canonically — sorted keys, comma-separated,
// empty for the zero value — so it can stand as one component of a cache
// key. No valid annotation key contains a comma, and NewProjection drops any
// that does, which is what makes the rendering unambiguous.
func (p Projection) String() string { return strings.Join(p.annotationKeys, ",") }

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
