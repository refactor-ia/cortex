package catalog

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/refactor-ia/cortex/internal/safepath"
)

// LoadedCapability is a declared capability with its admission decision.
type LoadedCapability struct {
	Path      string
	Manifest  CapabilityManifest
	Admission AdmissionDecision
}

// LoadedFamily is a family manifest and its declared capabilities.
type LoadedFamily struct {
	Manifest     FamilyManifest
	Capabilities []LoadedCapability
}

// LoadFamily loads exactly the declared family and capability manifests beneath root.
func LoadFamily(root, familyManifestPath string, policy AdmissionPolicy) (LoadedFamily, error) {
	if _, err := approvedThirdPartyLicenses(policy); err != nil {
		return LoadedFamily{}, errors.New("catalog load: admission policy is invalid")
	}

	familyPath, err := requireRegularFile(root, familyManifestPath)
	if err != nil {
		return LoadedFamily{}, errors.New("catalog load: family manifest is unavailable")
	}
	familyData, err := os.ReadFile(familyPath)
	if err != nil {
		return LoadedFamily{}, errors.New("catalog load: family manifest is unavailable")
	}
	family, err := DecodeFamilyManifest(familyData)
	if err != nil {
		return LoadedFamily{}, errors.New("catalog load: family manifest is invalid")
	}
	if _, err := requireRegularFile(root, family.Router); err != nil {
		return LoadedFamily{}, errors.New("catalog load: family router is unavailable")
	}

	loaded := LoadedFamily{
		Manifest:     family,
		Capabilities: make([]LoadedCapability, 0, len(family.Capabilities)),
	}
	ids := make(map[string]struct{}, len(family.Capabilities))
	for _, path := range family.Capabilities {
		manifestPath, err := requireRegularFile(root, path)
		if err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability manifest is unavailable")
		}
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability manifest is unavailable")
		}
		capability, admission, err := EvaluateAdmission(data, policy)
		if err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability manifest is invalid")
		}
		if capability.Family != family.ID {
			return LoadedFamily{}, errors.New("catalog load: capability family does not match")
		}
		if _, exists := ids[capability.ID]; exists {
			return LoadedFamily{}, errors.New("catalog load: capability id is duplicate")
		}
		if _, err := requireRegularFile(root, capability.Source); err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability source is unavailable")
		}
		ids[capability.ID] = struct{}{}
		loaded.Capabilities = append(loaded.Capabilities, LoadedCapability{
			Path: path, Manifest: capability, Admission: admission,
		})
	}
	return loaded, nil
}

// LoadFamilyFS loads a declared family from an immutable fs.FS tree.
func LoadFamilyFS(assets fs.FS, familyManifestPath string, policy AdmissionPolicy) (LoadedFamily, error) {
	if _, err := approvedThirdPartyLicenses(policy); err != nil {
		return LoadedFamily{}, errors.New("catalog load: admission policy is invalid")
	}
	data, err := readRegularFSFile(assets, familyManifestPath)
	if err != nil {
		return LoadedFamily{}, errors.New("catalog load: family manifest is unavailable")
	}
	family, err := DecodeFamilyManifest(data)
	if err != nil {
		return LoadedFamily{}, errors.New("catalog load: family manifest is invalid")
	}
	if _, err := readRegularFSFile(assets, family.Router); err != nil {
		return LoadedFamily{}, errors.New("catalog load: family router is unavailable")
	}
	loaded := LoadedFamily{Manifest: family, Capabilities: make([]LoadedCapability, 0, len(family.Capabilities))}
	ids := make(map[string]struct{}, len(family.Capabilities))
	for _, path := range family.Capabilities {
		data, err := readRegularFSFile(assets, path)
		if err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability manifest is unavailable")
		}
		capability, admission, err := EvaluateAdmission(data, policy)
		if err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability manifest is invalid")
		}
		if capability.Family != family.ID {
			return LoadedFamily{}, errors.New("catalog load: capability family does not match")
		}
		if _, exists := ids[capability.ID]; exists {
			return LoadedFamily{}, errors.New("catalog load: capability id is duplicate")
		}
		if _, err := readRegularFSFile(assets, capability.Source); err != nil {
			return LoadedFamily{}, errors.New("catalog load: capability source is unavailable")
		}
		ids[capability.ID] = struct{}{}
		loaded.Capabilities = append(loaded.Capabilities, LoadedCapability{Path: path, Manifest: capability, Admission: admission})
	}
	return loaded, nil
}

func readRegularFSFile(assets fs.FS, candidate string) ([]byte, error) {
	if !fs.ValidPath(candidate) || (!canonicalPath(candidate, ".json") && !canonicalPath(candidate, ".md")) {
		return nil, errors.New("path is not a regular file")
	}
	parent, parts := ".", strings.Split(candidate, "/")
	for index, part := range parts {
		entries, err := fs.ReadDir(assets, parent)
		if err != nil {
			return nil, err
		}
		entry, found := findFSEntry(entries, part)
		if !found || entry.Type()&fs.ModeSymlink != 0 {
			return nil, errors.New("path is not a regular file")
		}
		if index < len(parts)-1 {
			if !entry.IsDir() {
				return nil, errors.New("path is not a regular file")
			}
			if parent == "." {
				parent = part
			} else {
				parent += "/" + part
			}
		}
	}
	info, err := fs.Stat(assets, candidate)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("path is not a regular file")
	}
	return fs.ReadFile(assets, candidate)
}

func findFSEntry(entries []fs.DirEntry, name string) (fs.DirEntry, bool) {
	for _, entry := range entries {
		if entry.Name() == name {
			return entry, true
		}
	}
	return nil, false
}

func requireRegularFile(root, candidate string) (string, error) {
	path, err := safepath.Resolve(root, candidate)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return path, nil
}
