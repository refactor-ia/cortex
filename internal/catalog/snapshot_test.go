package catalog

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func buildSnapshot(t *testing.T, root string, policy AdmissionPolicy) CatalogSnapshot {
	t.Helper()
	snapshot, err := BuildCatalogSnapshot(root, "catalog.json", policy)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestBuildCatalogSnapshotMaterializesAdmittedCatalog(t *testing.T) {
	root, _ := catalogLoadFixture(t)
	snapshot := buildSnapshot(t, root, AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}})
	families := snapshot.Families()
	if len(families) != len(approvedFamilyIDs) || len(snapshot.Manifest().Families) != len(approvedFamilyIDs) {
		t.Fatalf("snapshot families = %d, want %d", len(families), len(approvedFamilyIDs))
	}
	for index, family := range families {
		if family.Manifest().ID != approvedFamilyIDs[index] || string(family.Router().Content()) != approvedFamilyIDs[index] {
			t.Fatalf("family %d = %q", index, family.Manifest().ID)
		}
	}
	capabilities := families[0].Capabilities()
	if len(capabilities) != 2 || capabilities[0].Manifest().ID != "owned" || capabilities[1].Manifest().ID != "approved" {
		t.Fatalf("admitted capabilities = %+v", capabilities)
	}
	for _, capability := range capabilities {
		if string(capability.Source().Content()) != capability.Manifest().ID {
			t.Fatalf("source for %q differs", capability.Manifest().ID)
		}
	}
	for _, family := range families {
		for _, capability := range family.Capabilities() {
			if capability.Manifest().ID == "rejected" {
				t.Fatal("rejected capability exposed")
			}
		}
	}
}

func TestCatalogSnapshotFingerprintInputsAndFileHashes(t *testing.T) {
	policy := AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT", "Apache-2.0"}}
	root, paths := catalogLoadFixture(t)
	original := buildSnapshot(t, root, policy)
	repeated := buildSnapshot(t, root, AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"Apache-2.0", "MIT"}})
	if got := original.Fingerprint(); got != repeated.Fingerprint() || len(got) != 64 || strings.Trim(got, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint = %q", got)
	}
	for _, family := range original.Families() {
		for _, file := range append([]CatalogFileSnapshot{family.Router()}, sources(family.Capabilities())...) {
			want := fmt.Sprintf("%x", sha256.Sum256(file.Content()))
			if file.SHA256() != want {
				t.Fatalf("file hash = %q, want %q", file.SHA256(), want)
			}
		}
	}
	cases := []struct {
		name   string
		modify func()
	}{
		{"router", func() { writeLoadFile(t, root, "routers/reasoning.md", "changed-router") }},
		{"admitted source", func() { writeLoadFile(t, root, "sources/owned.md", "changed-owned") }},
		{"description", func() {
			data := loadCapabilityJSON("owned", "reasoning", "sources/owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0")
			writeLoadFile(t, root, "manifests/owned.json", strings.Replace(data, "Professional capability description.", "Changed professional capability description.", 1))
		}},
		{"manifest admission", func() {
			writeLoadFile(t, root, "manifests/approved.json", loadCapabilityJSON("approved", "reasoning", "sources/approved.md", ProvenanceThirdParty, "GPL-3.0-only"))
		}},
		{"declared order", func() {
			writeLoadFile(t, root, paths["reasoning"], familyJSON("reasoning", "routers/reasoning.md", capabilityList([]string{"manifests/approved.json", "manifests/owned.json", "manifests/rejected.json"}), `[]`))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, paths = catalogLoadFixture(t)
			original = buildSnapshot(t, root, policy)
			tc.modify()
			if got := buildSnapshot(t, root, policy).Fingerprint(); got == original.Fingerprint() {
				t.Fatal("fingerprint did not change")
			}
		})
	}
	root, _ = catalogLoadFixture(t)
	original = buildSnapshot(t, root, policy)
	writeLoadFile(t, root, "sources/rejected.md", "changed-rejected")
	after := buildSnapshot(t, root, policy)
	if after.Fingerprint() != original.Fingerprint() || strings.Contains(string(snapshotContent(after)), "changed-rejected") {
		t.Fatal("rejected source affected the snapshot")
	}
}

func TestCatalogSnapshotAccessorsAreIsolated(t *testing.T) {
	snapshot := buildSnapshot(t, catalogRoot(t), AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}})
	fingerprint := snapshot.Fingerprint()
	families := snapshot.Families()
	families[0] = CatalogFamilySnapshot{}
	manifest := snapshot.Manifest()
	manifest.Families[0].ID = "changed"
	familyManifest := snapshot.Families()[0].Manifest()
	familyManifest.Capabilities = append(familyManifest.Capabilities, "changed.json")
	content := snapshot.Families()[0].Router().Content()
	content[0] = 'X'
	if snapshot.Fingerprint() != fingerprint || snapshot.Manifest().Families[0].ID != "reasoning" || len(snapshot.Families()[0].Manifest().Capabilities) != 3 || string(snapshot.Families()[0].Router().Content()) != "reasoning" {
		t.Fatal("accessor mutation changed snapshot")
	}
}

func TestBuildCatalogSnapshotErrorsAreZeroAndSafe(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		edit func(t *testing.T, root string, paths map[string]string)
	}{
		{"invalid policy", "catalog.json", nil},
		{"invalid root", "catalog.json", nil},
		{"delegated load", "catalog.json", func(t *testing.T, root string, paths map[string]string) {
			writeLoadFile(t, root, paths["reasoning"], "{")
		}},
		{"materialization", "catalog.json", func(t *testing.T, root string, _ map[string]string) {
			replaceWithDirectory(t, root, "routers/reasoning.md")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, paths := catalogLoadFixture(t)
			if tc.edit != nil {
				tc.edit(t, root, paths)
			}
			policy := AdmissionPolicy{}
			if tc.name == "invalid root" {
				root = "missing-root"
			}
			if tc.name == "invalid policy" {
				policy.CompatibleThirdPartyLicenses = []string{"MIT", "MIT"}
			}
			snapshot, err := BuildCatalogSnapshot(root, tc.path, policy)
			if err == nil || !reflect.DeepEqual(snapshot, CatalogSnapshot{}) {
				t.Fatalf("result = (%+v, %v)", snapshot, err)
			}
			for _, secret := range []string{root, "https", "password", "example.invalid", "license"} {
				if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secret)) {
					t.Fatalf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
	root, _ := catalogLoadFixture(t)
	writeLoadFile(t, root, "unreferenced/invalid.json", "{")
	if buildSnapshot(t, root, AdmissionPolicy{}).Fingerprint() == "" {
		t.Fatal("extra file prevented snapshot")
	}
}

func catalogRoot(t *testing.T) string { root, _ := catalogLoadFixture(t); return root }
func sources(capabilities []CatalogCapabilitySnapshot) []CatalogFileSnapshot {
	files := make([]CatalogFileSnapshot, len(capabilities))
	for i, capability := range capabilities {
		files[i] = capability.Source()
	}
	return files
}
func snapshotContent(snapshot CatalogSnapshot) []byte {
	var content []byte
	for _, family := range snapshot.Families() {
		content = append(content, family.Router().Content()...)
		for _, capability := range family.Capabilities() {
			content = append(content, capability.Source().Content()...)
		}
	}
	return content
}
