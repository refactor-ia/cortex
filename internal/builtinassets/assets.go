// Package builtinassets provides Cortex's embedded lifecycle catalog.
package builtinassets

import (
	"embed"
	"io/fs"

	"github.com/refactor-ia/cortex/internal/catalog"
)

//go:embed catalog
var assets embed.FS

// Snapshot loads the Cortex-owned embedded catalog.
func Snapshot() (catalog.CatalogSnapshot, error) {
	catalogFS, err := fs.Sub(assets, "catalog")
	if err != nil {
		return catalog.CatalogSnapshot{}, err
	}
	return catalog.BuildCatalogSnapshotFS(catalogFS, "catalog.json", catalog.AdmissionPolicy{})
}
