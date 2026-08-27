package catalog

import (
	"errors"
	"os"
)

// LoadedCatalog is a root catalog manifest with each declared family loaded in
// canonical approved-family order.
type LoadedCatalog struct {
	Manifest CatalogManifest
	Families []LoadedFamily
}

// LoadCatalog loads exactly the root catalog and the family manifests it
// declares beneath root. Like LoadFamily, its Lstat-to-ReadFile sequence leaves
// the primitive residual TOCTOU race between checking a path and reading it.
func LoadCatalog(root, catalogManifestPath string, policy AdmissionPolicy) (LoadedCatalog, error) {
	if _, err := approvedThirdPartyLicenses(policy); err != nil {
		return LoadedCatalog{}, errors.New("catalog load: admission policy is invalid")
	}
	if !canonicalPath(catalogManifestPath, ".json") {
		return LoadedCatalog{}, errors.New("catalog load: catalog manifest path is invalid")
	}

	path, err := requireRegularFile(root, catalogManifestPath)
	if err != nil {
		return LoadedCatalog{}, errors.New("catalog load: catalog manifest is unavailable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return LoadedCatalog{}, errors.New("catalog load: catalog manifest is unavailable")
	}
	manifest, err := DecodeCatalogManifest(data)
	if err != nil {
		return LoadedCatalog{}, errors.New("catalog load: catalog manifest is invalid")
	}

	loaded := LoadedCatalog{
		Manifest: manifest,
		Families: make([]LoadedFamily, 0, len(manifest.Families)),
	}
	capabilityIDs := make(map[string]struct{})
	for _, reference := range manifest.Families {
		family, err := LoadFamily(root, reference.ManifestPath, policy)
		if err != nil {
			return LoadedCatalog{}, errors.New("catalog load: family manifest is invalid")
		}
		if family.Manifest.ID != reference.ID {
			return LoadedCatalog{}, errors.New("catalog load: family id does not match catalog reference")
		}
		for _, capability := range family.Capabilities {
			if _, exists := capabilityIDs[capability.Manifest.ID]; exists {
				return LoadedCatalog{}, errors.New("catalog load: capability id is duplicate")
			}
			capabilityIDs[capability.Manifest.ID] = struct{}{}
		}
		loaded.Families = append(loaded.Families, family)
	}
	return loaded, nil
}
