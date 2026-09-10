package k8s

import (
	"go/ast"
	"go/token"
	"testing"
)

// TestNoLimitedListAsksForTheWatchCache is a drift guard over a pairing the
// API server accepts and then ignores.
//
// A LIST that names both a Limit and ResourceVersion "0" has its limit
// DROPPED by the server: ShouldDelegateList excludes "0" from the branch that
// would send a limited list to etcd, so the watch cache answers it, and
// computeListLimit returns 0 whenever the resource version is "0". No error
// comes back. The response simply contains the whole collection — and no
// Continue token, so a paging loop reads one page, finds no token, and
// reports itself complete having read everything.
//
// THREE READS IN THIS PACKAGE WERE WRITTEN THAT WAY, and one of them was the
// Helm listing, where the cap that did not exist governed how many Secrets
// the API server was asked to decrypt. Nothing failed, nothing logged, and
// the constants naming the caps read as if they were in force.
//
// The rule is one line: a read that names a Limit must not name
// cachedResourceVersion. Giving up the watch cache there is not giving up
// much — it is the price of a cap that binds and a Continue token that
// exists — and a read with no Limit still uses it freely.
func TestNoLimitedListAsksForTheWatchCache(t *testing.T) {
	files, fset := parsePackage(t)

	found := 0
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isListOptions(literal.Type) {
				return true
			}

			var limited, cached bool
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok {
					continue
				}
				switch key.Name {
				case "Limit":
					limited = true
				case "ResourceVersion":
					if value, ok := pair.Value.(*ast.Ident); ok && value.Name == "cachedResourceVersion" {
						cached = true
					}
				}
			}

			if limited && cached {
				found++
				t.Errorf("%s: a ListOptions names both Limit and cachedResourceVersion — "+
					"the server drops the limit and returns the whole collection with no Continue token",
					position(fset, literal.Pos()))
			}
			return true
		})
	}

	if found == 0 {
		t.Log("no limited list asks for the watch cache")
	}
}

// position renders a node's file and line.
func position(fset *token.FileSet, pos token.Pos) string {
	return fset.Position(pos).String()
}

// isListOptions reports whether a composite literal's type is
// metav1.ListOptions.
func isListOptions(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ListOptions" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "metav1"
}
