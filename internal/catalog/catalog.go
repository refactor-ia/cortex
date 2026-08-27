package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// CatalogFamilyReference identifies one family manifest in a catalog.
type CatalogFamilyReference struct {
	ID           string
	ManifestPath string
}

// CatalogManifest is a catalog manifest version 1.
type CatalogManifest struct {
	SchemaVersion int
	Families      []CatalogFamilyReference
}

type catalogManifestWire struct {
	SchemaVersion *int                        `json:"schemaVersion"`
	Families      *map[string]json.RawMessage `json:"families"`
}

// DecodeCatalogManifest decodes and validates a version 1 catalog manifest.
func DecodeCatalogManifest(data []byte) (CatalogManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire catalogManifestWire
	if err := decoder.Decode(&wire); err != nil {
		return CatalogManifest{}, fmt.Errorf("decode catalog manifest: invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CatalogManifest{}, fmt.Errorf("decode catalog manifest: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.Families == nil {
		return CatalogManifest{}, fmt.Errorf("catalog manifest: required field is missing")
	}
	if *wire.SchemaVersion != 1 {
		return CatalogManifest{}, fmt.Errorf("catalog manifest: schemaVersion must be 1")
	}
	if len(*wire.Families) != len(approvedFamilyIDs) {
		return CatalogManifest{}, fmt.Errorf("catalog manifest: families must contain exactly the approved IDs")
	}

	families := make([]CatalogFamilyReference, 0, len(approvedFamilyIDs))
	for _, id := range approvedFamilyIDs {
		rawPath, exists := (*wire.Families)[id]
		if !exists || len(rawPath) < 2 || rawPath[0] != '"' {
			return CatalogManifest{}, fmt.Errorf("catalog manifest: families must contain string paths")
		}
		var path string
		if err := json.Unmarshal(rawPath, &path); err != nil || !canonicalPath(path, ".json") {
			return CatalogManifest{}, fmt.Errorf("catalog manifest: families must contain canonical .json paths")
		}
		families = append(families, CatalogFamilyReference{ID: id, ManifestPath: path})
	}
	return CatalogManifest{SchemaVersion: *wire.SchemaVersion, Families: families}, nil
}
