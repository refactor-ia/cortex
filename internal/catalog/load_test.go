package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadCapabilityJSON(id, family, source, provenance, license string) string {
	urls := ""
	if provenance == ProvenanceThirdParty {
		urls = `,"provenanceUrl":"https://example.com/source","redistributionUrl":"https://example.com/license"`
	}
	return fmt.Sprintf(`{"schemaVersion":1,"id":%q,"description":"Professional capability description.","family":%q,"source":%q,"activation":"automatic","provenance":%q,"license":%q,"redistributionAllowed":true%s}`,
		id, family, source, provenance, license, urls)
}

func writeLoadFile(t *testing.T, root, path, contents string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func loadFixture(t *testing.T, capabilities []string) string {
	t.Helper()
	root := t.TempDir()
	writeLoadFile(t, root, "family.json", familyJSON("reasoning", "routers/reasoning.md", capabilityList(capabilities), `[]`))
	writeLoadFile(t, root, "routers/reasoning.md", "router")
	for _, capability := range capabilities {
		switch capability {
		case "manifests/owned.json":
			writeLoadFile(t, root, capability, loadCapabilityJSON("owned", "reasoning", "sources/owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
			writeLoadFile(t, root, "sources/owned.md", "owned")
		case "manifests/approved.json":
			writeLoadFile(t, root, capability, loadCapabilityJSON("approved", "reasoning", "sources/approved.md", ProvenanceThirdParty, "MIT"))
			writeLoadFile(t, root, "sources/approved.md", "approved")
		case "manifests/rejected.json":
			writeLoadFile(t, root, capability, loadCapabilityJSON("rejected", "reasoning", "sources/rejected.md", ProvenanceThirdParty, "GPL-3.0-only"))
			writeLoadFile(t, root, "sources/rejected.md", "rejected")
		}
	}
	return root
}

func capabilityList(paths []string) string {
	values := make([]string, len(paths))
	for index, path := range paths {
		values[index] = fmt.Sprintf("%q", path)
	}
	return "[" + strings.Join(values, ",") + "]"
}

func TestLoadFamilyPreservesDeclaredCapabilitiesAndDecisions(t *testing.T) {
	paths := []string{"manifests/owned.json", "manifests/approved.json", "manifests/rejected.json"}
	loaded, err := LoadFamily(loadFixture(t, paths), "family.json", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.ID != "reasoning" || len(loaded.Capabilities) != len(paths) {
		t.Fatalf("LoadFamily() = %+v", loaded)
	}
	for index, want := range []struct {
		path, id string
		admitted bool
		reason   AdmissionReason
	}{
		{paths[0], "owned", true, AdmissionReasonCortexOwned},
		{paths[1], "approved", true, AdmissionReasonThirdPartyLicenseApproved},
		{paths[2], "rejected", false, AdmissionReasonThirdPartyLicenseRejected},
	} {
		got := loaded.Capabilities[index]
		if got.Path != want.path || got.Manifest.ID != want.id || got.Admission.Admitted != want.admitted || got.Admission.Reason != want.reason {
			t.Errorf("capability %d = %+v, want %q/%q/%t/%q", index, got, want.path, want.id, want.admitted, want.reason)
		}
	}
}

func TestLoadFamilyEmptyValidatesPolicyAndIgnoresUnreferencedFiles(t *testing.T) {
	root := loadFixture(t, nil)
	writeLoadFile(t, root, "unreferenced.json", "not a manifest")
	loaded, err := LoadFamily(root, "family.json", AdmissionPolicy{})
	if err != nil || loaded.Capabilities == nil || len(loaded.Capabilities) != 0 {
		t.Fatalf("LoadFamily() = (%+v, %v), want empty non-nil capabilities", loaded, err)
	}
	if _, err := LoadFamily(root, "family.json", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT", "MIT"}}); err == nil {
		t.Fatal("LoadFamily() invalid empty-family policy error = nil")
	}
}

func TestLoadFamilyRejectsMissingUnsafeSymlinkAndDirectoryPaths(t *testing.T) {
	paths := []string{"manifests/owned.json"}
	for _, tc := range []struct {
		name, path string
		modify     func(t *testing.T, root string)
	}{
		{"missing family", "missing.json", nil},
		{"unsafe family", "../family.json", nil},
		{"family directory", "family.json", func(t *testing.T, root string) { replaceWithDirectory(t, root, "family.json") }},
		{"family symlink", "family.json", func(t *testing.T, root string) { replaceWithSymlink(t, root, "family.json") }},
		{"missing router", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "family.json", familyJSON("reasoning", "missing.md", capabilityList(paths), `[]`))
		}},
		{"unsafe router", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "family.json", familyJSON("reasoning", "../router.md", capabilityList(paths), `[]`))
		}},
		{"router directory", "family.json", func(t *testing.T, root string) { replaceWithDirectory(t, root, "routers/reasoning.md") }},
		{"router symlink", "family.json", func(t *testing.T, root string) { replaceWithSymlink(t, root, "routers/reasoning.md") }},
		{"missing capability", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "family.json", familyJSON("reasoning", "routers/reasoning.md", `["missing.json"]`, `[]`))
		}},
		{"unsafe capability", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "family.json", familyJSON("reasoning", "routers/reasoning.md", `["../owned.json"]`, `[]`))
		}},
		{"capability directory", "family.json", func(t *testing.T, root string) { replaceWithDirectory(t, root, "manifests/owned.json") }},
		{"capability symlink", "family.json", func(t *testing.T, root string) { replaceWithSymlink(t, root, "manifests/owned.json") }},
		{"missing source", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "manifests/owned.json", loadCapabilityJSON("owned", "reasoning", "missing.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
		}},
		{"unsafe source", "family.json", func(t *testing.T, root string) {
			writeLoadFile(t, root, "manifests/owned.json", loadCapabilityJSON("owned", "reasoning", "../owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
		}},
		{"source directory", "family.json", func(t *testing.T, root string) { replaceWithDirectory(t, root, "sources/owned.md") }},
		{"source symlink", "family.json", func(t *testing.T, root string) { replaceWithSymlink(t, root, "sources/owned.md") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loadFixture(t, paths)
			if tc.modify != nil {
				tc.modify(t, root)
			}
			if _, err := LoadFamily(root, tc.path, AdmissionPolicy{}); err == nil {
				t.Fatal("LoadFamily() error = nil")
			}
		})
	}
}

func replaceWithDirectory(t *testing.T, root, path string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, path), 0o755); err != nil {
		t.Fatal(err)
	}
}

func replaceWithSymlink(t *testing.T, root, path string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, path)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFamilyRejectsInvalidCrossFamilyDuplicateAndLeakingInput(t *testing.T) {
	paths := []string{"manifests/owned.json"}
	for _, tc := range []struct {
		name      string
		modify    func(t *testing.T, root string)
		secrets   []string
		checkRoot bool
	}{
		{"invalid family", func(t *testing.T, root string) { writeLoadFile(t, root, "family.json", "{") }, nil, false},
		{"invalid capability", func(t *testing.T, root string) { writeLoadFile(t, root, "manifests/owned.json", "{") }, nil, false},
		{"missing description", func(t *testing.T, root string) {
			data := loadCapabilityJSON("owned", "reasoning", "sources/owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0")
			writeLoadFile(t, root, "manifests/owned.json", strings.Replace(data, `,"description":"Professional capability description."`, "", 1))
		}, nil, false},
		{"cross family", func(t *testing.T, root string) {
			writeLoadFile(t, root, "manifests/owned.json", loadCapabilityJSON("owned", "web", "sources/owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
		}, nil, false},
		{"duplicate ids", func(t *testing.T, root string) {
			writeLoadFile(t, root, "family.json", familyJSON("reasoning", "routers/reasoning.md", `["manifests/owned.json","manifests/second.json"]`, `[]`))
			writeLoadFile(t, root, "manifests/second.json", loadCapabilityJSON("owned", "reasoning", "sources/second.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
			writeLoadFile(t, root, "sources/second.md", "second")
		}, nil, false},
		{"safe error", func(t *testing.T, root string) {
			writeLoadFile(t, root, "manifests/owned.json", `{"license":"private-license","provenanceUrl":"https://private.example/secret"}`)
		}, []string{"private-license", "https://private.example/secret"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := loadFixture(t, paths)
			tc.modify(t, root)
			_, err := LoadFamily(root, "family.json", AdmissionPolicy{})
			if err == nil {
				t.Fatal("LoadFamily() error = nil")
			}
			for _, secret := range tc.secrets {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("LoadFamily() leaked %q in %q", secret, err)
				}
			}
			if tc.checkRoot && strings.Contains(err.Error(), root) {
				t.Errorf("LoadFamily() leaked absolute root in %q", err)
			}
		})
	}
}
