package releasecatalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/releasecatalog"
)

func TestBuiltInSourceResolvesSnapshotsWithoutCallerEvidence(t *testing.T) {
	source := releasecatalog.BuiltInSource()
	if _, err := source.ResolveSnapshot(catalog.CatalogSnapshot{}); err == nil {
		t.Fatal("BuiltInSource().ResolveSnapshot() error = nil, want rejection")
	}
}

func TestResolutionIsOpaqueOutput(t *testing.T) {
	zero := releasecatalog.Resolution{}
	if got := zero.ID(); got != "" {
		t.Errorf("zero Resolution ID() = %q, want empty", got)
	}
	if got := zero.CatalogVersion(); got != 0 {
		t.Errorf("zero Resolution CatalogVersion() = %d, want zero", got)
	}
	if got := zero.Fingerprint(); got != "" {
		t.Errorf("zero Resolution Fingerprint() = %q, want empty", got)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(testFile), "source.go"), nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(source.go) error = %v", err)
	}
	for _, declaration := range file.Decls {
		declaration, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range declaration.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "Resolution" {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatal("Resolution is not a struct")
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					if name.IsExported() {
						t.Errorf("Resolution unexpectedly exposes mutable field %s", name.Name)
					}
				}
			}
			return
		}
	}
	t.Fatal("Resolution type declaration not found")
}

func TestPublicAPIExposesOnlyBuiltInSnapshotResolution(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(testFile), "source.go"), nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(source.go) error = %v", err)
	}

	exported := make(map[string]bool)
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if declaration.Name.IsExported() {
				exported[declaration.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, specification := range declaration.Specs {
				if typeSpec, ok := specification.(*ast.TypeSpec); ok && typeSpec.Name.IsExported() {
					exported[typeSpec.Name.Name] = true
				}
			}
		}
	}
	for _, name := range []string{"BuiltInSource", "Resolution", "ResolveSnapshot", "ID", "CatalogVersion", "Fingerprint"} {
		if !exported[name] {
			t.Errorf("public API does not expose %s", name)
		}
		delete(exported, name)
	}
	for name := range exported {
		t.Errorf("public API unexpectedly exposes %s", name)
	}
}
