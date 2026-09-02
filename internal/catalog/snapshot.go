package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"io/fs"
	"os"
)

// CatalogSnapshot is an immutable, admitted materialization of a loaded catalog.
type CatalogSnapshot struct {
	manifest    CatalogManifest
	families    []CatalogFamilySnapshot
	fingerprint string
}

// CatalogFamilySnapshot is an immutable materialization of one family.
type CatalogFamilySnapshot struct {
	manifest     FamilyManifest
	router       CatalogFileSnapshot
	capabilities []CatalogCapabilitySnapshot
}

// CatalogCapabilitySnapshot is an immutable materialization of an admitted capability.
type CatalogCapabilitySnapshot struct {
	manifest CapabilityManifest
	source   CatalogFileSnapshot
}

// CatalogFileSnapshot is an immutable materialized regular file.
type CatalogFileSnapshot struct {
	path    string
	content string
	sha256  string
}

// BuildCatalogSnapshot loads, admits, and materializes the catalog rooted at root.
// Like LoadCatalog, its Lstat-to-ReadFile checks retain the residual TOCTOU race;
// this is not a filesystem-atomic snapshot.
func BuildCatalogSnapshot(root, catalogManifestPath string, policy AdmissionPolicy) (CatalogSnapshot, error) {
	loaded, err := LoadCatalog(root, catalogManifestPath, policy)
	if err != nil {
		return CatalogSnapshot{}, errors.New("catalog snapshot: catalog load failed")
	}
	return buildCatalogSnapshot(loaded, func(path string) (CatalogFileSnapshot, error) {
		return materializeCatalogFile(root, path)
	})
}

// BuildCatalogSnapshotFS loads, admits, and materializes a catalog from an fs.FS.
// Mutable fs.FS implementations retain the read-to-read consistency/TOCTOU residual;
// immutable embed.FS does not.
func BuildCatalogSnapshotFS(assets fs.FS, catalogManifestPath string, policy AdmissionPolicy) (CatalogSnapshot, error) {
	loaded, err := LoadCatalogFS(assets, catalogManifestPath, policy)
	if err != nil {
		return CatalogSnapshot{}, errors.New("catalog snapshot: catalog load failed")
	}
	return buildCatalogSnapshot(loaded, func(path string) (CatalogFileSnapshot, error) {
		return materializeCatalogFSFile(assets, path)
	})
}

func buildCatalogSnapshot(loaded LoadedCatalog, materialize func(string) (CatalogFileSnapshot, error)) (CatalogSnapshot, error) {
	families := make([]CatalogFamilySnapshot, 0, len(loaded.Families))
	for _, loadedFamily := range loaded.Families {
		router, err := materialize(loadedFamily.Manifest.Router)
		if err != nil {
			return CatalogSnapshot{}, errors.New("catalog snapshot: materialization failed")
		}
		family := CatalogFamilySnapshot{manifest: cloneFamilyManifest(loadedFamily.Manifest), router: router, capabilities: make([]CatalogCapabilitySnapshot, 0, len(loadedFamily.Capabilities))}
		for _, loadedCapability := range loadedFamily.Capabilities {
			if !loadedCapability.Admission.Admitted {
				continue
			}
			source, err := materialize(loadedCapability.Manifest.Source)
			if err != nil {
				return CatalogSnapshot{}, errors.New("catalog snapshot: materialization failed")
			}
			family.capabilities = append(family.capabilities, CatalogCapabilitySnapshot{manifest: loadedCapability.Manifest, source: source})
		}
		families = append(families, family)
	}
	return CatalogSnapshot{manifest: cloneCatalogManifest(loaded.Manifest), families: families, fingerprint: fingerprintCatalog(loaded, families)}, nil
}

func materializeCatalogFile(root, candidate string) (CatalogFileSnapshot, error) {
	path, err := requireRegularFile(root, candidate)
	if err != nil {
		return CatalogFileSnapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CatalogFileSnapshot{}, err
	}
	return catalogFileSnapshot(candidate, data), nil
}

func materializeCatalogFSFile(assets fs.FS, candidate string) (CatalogFileSnapshot, error) {
	data, err := readRegularFSFile(assets, candidate)
	if err != nil {
		return CatalogFileSnapshot{}, err
	}
	return catalogFileSnapshot(candidate, data), nil
}

func catalogFileSnapshot(path string, data []byte) CatalogFileSnapshot {
	digest := sha256.Sum256(data)
	return CatalogFileSnapshot{path: path, content: string(data), sha256: hex.EncodeToString(digest[:])}
}

// Manifest returns a deep copy of the catalog manifest.
func (snapshot CatalogSnapshot) Manifest() CatalogManifest {
	return cloneCatalogManifest(snapshot.manifest)
}

// Families returns immutable family snapshots in canonical catalog order.
func (snapshot CatalogSnapshot) Families() []CatalogFamilySnapshot {
	return append([]CatalogFamilySnapshot(nil), snapshot.families...)
}

// Fingerprint returns the deterministic SHA-256 identity of this snapshot.
func (snapshot CatalogSnapshot) Fingerprint() string { return snapshot.fingerprint }

// Manifest returns a deep copy of the family manifest.
func (snapshot CatalogFamilySnapshot) Manifest() FamilyManifest {
	return cloneFamilyManifest(snapshot.manifest)
}

// Router returns the materialized family router.
func (snapshot CatalogFamilySnapshot) Router() CatalogFileSnapshot { return snapshot.router }

// Capabilities returns admitted capability snapshots in declaration order.
func (snapshot CatalogFamilySnapshot) Capabilities() []CatalogCapabilitySnapshot {
	return append([]CatalogCapabilitySnapshot(nil), snapshot.capabilities...)
}

// Manifest returns a copy of the capability manifest.
func (snapshot CatalogCapabilitySnapshot) Manifest() CapabilityManifest { return snapshot.manifest }

// Source returns the materialized admitted capability source.
func (snapshot CatalogCapabilitySnapshot) Source() CatalogFileSnapshot { return snapshot.source }

// Path returns the catalog-relative path of the materialized file.
func (snapshot CatalogFileSnapshot) Path() string { return snapshot.path }

// Content returns a copy of the materialized file bytes.
func (snapshot CatalogFileSnapshot) Content() []byte { return []byte(snapshot.content) }

// SHA256 returns the lowercase SHA-256 hash of Content.
func (snapshot CatalogFileSnapshot) SHA256() string { return snapshot.sha256 }

func cloneCatalogManifest(manifest CatalogManifest) CatalogManifest {
	return CatalogManifest{SchemaVersion: manifest.SchemaVersion, Families: append([]CatalogFamilyReference(nil), manifest.Families...)}
}

func cloneFamilyManifest(manifest FamilyManifest) FamilyManifest {
	return FamilyManifest{SchemaVersion: manifest.SchemaVersion, ID: manifest.ID, Router: manifest.Router, Capabilities: append([]string(nil), manifest.Capabilities...), Agents: append([]string(nil), manifest.Agents...)}
}

func fingerprintCatalog(loaded LoadedCatalog, families []CatalogFamilySnapshot) string {
	hasher := sha256.New()
	writeFrame(hasher, []byte("cortex.catalog.snapshot.v1"))
	writeUint64(hasher, uint64(loaded.Manifest.SchemaVersion))
	writeUint64(hasher, uint64(len(loaded.Manifest.Families)))
	for _, reference := range loaded.Manifest.Families {
		writeString(hasher, reference.ID)
		writeString(hasher, reference.ManifestPath)
	}
	for index, family := range loaded.Families {
		manifest := family.Manifest
		writeUint64(hasher, uint64(manifest.SchemaVersion))
		writeString(hasher, manifest.ID)
		writeString(hasher, manifest.Router)
		writeUint64(hasher, uint64(len(manifest.Capabilities)))
		for _, path := range manifest.Capabilities {
			writeString(hasher, path)
		}
		writeUint64(hasher, uint64(len(manifest.Agents)))
		for _, agent := range manifest.Agents {
			writeString(hasher, agent)
		}
		writeString(hasher, families[index].router.path)
		writeFrame(hasher, []byte(families[index].router.content))
		admitted := 0
		for _, capability := range family.Capabilities {
			writeCapability(hasher, capability)
			if capability.Admission.Admitted {
				writeString(hasher, families[index].capabilities[admitted].source.path)
				writeFrame(hasher, []byte(families[index].capabilities[admitted].source.content))
				admitted++
			}
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func writeCapability(hasher hash.Hash, capability LoadedCapability) {
	manifest := capability.Manifest
	writeString(hasher, capability.Path)
	writeUint64(hasher, uint64(manifest.SchemaVersion))
	for _, value := range []string{manifest.ID, manifest.Description, manifest.Family, manifest.Source, manifest.Activation, manifest.Provenance, manifest.License} {
		writeString(hasher, value)
	}
	writeBool(hasher, manifest.RedistributionAllowed)
	writeString(hasher, manifest.ProvenanceURL)
	writeString(hasher, manifest.RedistributionURL)
	writeBool(hasher, capability.Admission.Admitted)
	writeString(hasher, string(capability.Admission.Reason))
}

func writeString(hasher hash.Hash, value string) { writeFrame(hasher, []byte(value)) }
func writeBool(hasher hash.Hash, value bool) {
	if value {
		writeFrame(hasher, []byte{1})
		return
	}
	writeFrame(hasher, []byte{0})
}
func writeUint64(hasher hash.Hash, value uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	writeFrame(hasher, data[:])
}
func writeFrame(hasher hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(value)
}
