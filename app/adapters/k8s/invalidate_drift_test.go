package k8s

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryPerClusterCacheIsDroppedOnInvalidate is a drift guard, not a unit
// test of any one cache.
//
// THE BUG IT EXISTS FOR. Invalidate names each cache by hand, so adding a
// cache to the Adapter is not the same act as dropping it on a disconnect —
// and nothing failed when the two came apart. backendCache went unlisted with
// the longest TTL in the file, half an hour, holding a Service coordinate: a
// tab reconnected onto a different kubeconfig context went on proxying PromQL
// to an address discovered in the cluster it used to be.
//
// The rule this pins is the one a reader would assume anyway: if a field of
// the Adapter can forget one cluster, Invalidate makes it. Adding a cache
// with a forget method and not listing it below fails here rather than in
// somebody's graphs a month later.
func TestEveryPerClusterCacheIsDroppedOnInvalidate(t *testing.T) {
	files := parsePackage(t)

	// Types that can forget one cluster at all.
	forgetful := map[string]bool{}
	// Adapter fields, by name and by type.
	fields := map[string]string{}
	var invalidate *ast.FuncDecl

	for _, file := range files {
		for _, decl := range file.Decls {
			switch node := decl.(type) {
			case *ast.FuncDecl:
				receiver := receiverType(node)
				if node.Name.Name == "forget" && receiver != "" {
					forgetful[receiver] = true
				}
				if node.Name.Name == "Invalidate" && receiver == "Adapter" {
					invalidate = node
				}
			case *ast.GenDecl:
				collectAdapterFields(node, fields)
			}
		}
	}

	if invalidate == nil {
		t.Fatal("no (*Adapter).Invalidate found — this guard is reading the wrong package")
	}
	if len(fields) == 0 {
		t.Fatal("no Adapter fields found — this guard is reading the wrong package")
	}

	dropped := forgottenIn(invalidate)

	for name, typeName := range fields {
		if !forgetful[typeName] || dropped[name] {
			continue
		}
		t.Errorf("Adapter.%s (%s) can forget a cluster but Invalidate never asks it to — "+
			"its answers survive a disconnect and describe the cluster this tab used to be",
			name, typeName)
	}
}

// parsePackage reads this package's non-test files.
func parsePackage(t *testing.T) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files = append(files, file)
	}
	return files
}

// receiverType names the type a method hangs off, pointer or not.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	return typeName(fn.Recv.List[0].Type)
}

// typeName unwraps a pointer and returns the identifier, or "".
func typeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

// collectAdapterFields records `type Adapter struct` field names and types.
func collectAdapterFields(decl *ast.GenDecl, into map[string]string) {
	for _, spec := range decl.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "Adapter" {
			continue
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range structType.Fields.List {
			for _, name := range field.Names {
				into[name.Name] = typeName(field.Type)
			}
		}
	}
}

// forgottenIn returns the receiver's field names that Invalidate calls forget
// on — `a.helm.forget(id)` yields "helm".
func forgottenIn(fn *ast.FuncDecl) map[string]bool {
	receiver := ""
	if fn.Recv != nil && len(fn.Recv.List) == 1 && len(fn.Recv.List[0].Names) == 1 {
		receiver = fn.Recv.List[0].Names[0].Name
	}

	dropped := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != "forget" {
			return true
		}
		field, ok := method.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := field.X.(*ast.Ident); ok && ident.Name == receiver {
			dropped[field.Sel.Name] = true
		}
		return true
	})
	return dropped
}
