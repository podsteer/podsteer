package k8s

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"k8s.io/client-go/util/jsonpath"

	"github.com/podsteer/podsteer/app/domain"
)

// Evaluating an operator's own JSONPath columns.
//
// THE EXPRESSIONS ARE EVALUATED HERE, IN GO, AND ONLY THE ANSWERS CROSS THE
// BRIDGE. The alternative — sending whole objects to the webview and reading
// them there — would put a list's entire spec and status through the IPC
// boundary on every refresh tick, for a handful of cells. The extra bytes
// this feature costs are between PodSteer and the API server, where they are
// bounded by one list; the interface still receives a table.
//
// THE LIBRARY IS kubectl's OWN. `k8s.io/client-go/util/jsonpath` is what
// `kubectl get -o jsonpath` and `-o custom-columns` use, so an expression
// somebody already has in a script means the same thing here. Writing a
// second dialect of JSONPath — or borrowing a general-purpose one with
// different array and filter semantics — is how a column quietly disagrees
// with the command it was copied from.

// parsedExpressions caches compiled parsers, keyed by the expression text.
//
// A LIST RE-EVALUATES EVERY ROW ON EVERY TICK, and parsing the same handful
// of expressions five thousand times a second is work with no result. The map
// is bounded by how many distinct expressions an operator has ever typed in
// this session, which is a number of columns rather than a number of objects.
var parsedExpressions sync.Map

// customColumns evaluates every expression against one object.
//
// Nil — never an empty map — when nothing was asked for, matching what
// Projection.Annotations does and for the same reason: an object read from
// the watch store and the same object read from the network must map to
// values that are equal, not merely equivalent.
func customColumns(projection domain.Projection, object any) map[string]string {
	expressions := projection.Expressions()
	if len(expressions) == 0 || object == nil {
		return nil
	}

	values := make(map[string]string, len(expressions))
	for _, expression := range expressions {
		values[expression.ID] = evaluateExpression(expression.Path, object)
	}
	return values
}

// evaluateExpression runs one expression and renders its result.
//
// EVERY FAILURE IS A CELL, NOT AN ERROR. A path that does not parse, one that
// names a field this object does not have, one that matches nothing — none of
// them is a fault in the cluster or in PodSteer, and none is worth failing a
// list of five thousand rows over. A bad path says so in its own cell, once
// per row, where the operator who typed it is looking; a path that simply
// found nothing renders empty, exactly as a missing label does.
func evaluateExpression(path string, object any) string {
	parser, err := parserFor(path)
	if err != nil {
		return "!" + err.Error()
	}

	// THE OBJECT IS WALKED AS IT IS, typed struct or generic map.
	//
	// jsonpath resolves a struct's fields by their JSON tags, so
	// `.spec.containers[0].image` reads the same on a *corev1.Pod as on the
	// decoded map a server-printed table carries — measured, not assumed:
	// customcolumns_test.go asserts the same expression against both shapes.
	// Converting every object to `map[string]any` first would be one map
	// allocation per row per tick, on lists of five thousand, for no
	// difference in the answer.
	var out bytes.Buffer
	if err := parser.Execute(&out, object); err != nil {
		// A path that found nothing. AllowMissingKeys is on, so this is a
		// genuine mismatch — an index past the end, a field on the wrong
		// kind — and the honest rendering is the same as an absent label's.
		return ""
	}
	return strings.TrimSpace(out.String())
}

// parserFor compiles an expression, once.
func parserFor(path string) (*jsonpath.JSONPath, error) {
	if cached, found := parsedExpressions.Load(path); found {
		parser, ok := cached.(*jsonpath.JSONPath)
		if !ok {
			return nil, fmt.Errorf("bad expression")
		}
		return parser, nil
	}

	parser := jsonpath.New("column").AllowMissingKeys(true)
	// kubectl's own leniency: `.status.phase` and `{.status.phase}` are the
	// same expression, and people paste both. The library wants the braces.
	if err := parser.Parse(wrapExpression(path)); err != nil {
		return nil, fmt.Errorf("bad path")
	}

	parsedExpressions.Store(path, parser)
	return parser, nil
}

// wrapExpression puts an unbraced path into the braces the parser wants.
func wrapExpression(path string) string {
	trimmed := strings.TrimSpace(path)
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	return "{" + trimmed + "}"
}
