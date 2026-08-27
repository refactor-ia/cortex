package catalog

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func catalogLoadFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	paths := catalogPaths()
	for _, id := range approvedFamilyIDs {
		capabilities := []string(nil)
		if id == "reasoning" {
			capabilities = []string{"manifests/owned.json", "manifests/approved.json", "manifests/rejected.json"}
			writeLoadFile(t, root, capabilities[0], loadCapabilityJSON("owned", id, "sources/owned.md", ProvenanceCortexOwned, "CC-BY-SA-4.0"))
			writeLoadFile(t, root, capabilities[1], loadCapabilityJSON("approved", id, "sources/approved.md", ProvenanceThirdParty, "MIT"))
			writeLoadFile(t, root, capabilities[2], loadCapabilityJSON("rejected", id, "sources/rejected.md", ProvenanceThirdParty, "GPL-3.0-only"))
			for _, name := range []string{"owned", "approved", "rejected"} {
				writeLoadFile(t, root, "sources/"+name+".md", name)
			}
		}
		writeLoadFile(t, root, paths[id], familyJSON(id, "routers/"+id+".md", capabilityList(capabilities), `[]`))
		writeLoadFile(t, root, "routers/"+id+".md", id)
	}
	writeLoadFile(t, root, "catalog.json", catalogJSON(approvedFamilyIDs, paths))
	return root, paths
}

func catalogLoadError(t *testing.T, root, path string, policy AdmissionPolicy) {
	t.Helper()
	loaded, err := LoadCatalog(root, path, policy)
	if err == nil {
		t.Fatal("LoadCatalog() error = nil")
	}
	if loaded.Manifest.SchemaVersion != 0 || loaded.Manifest.Families != nil || loaded.Families != nil {
		t.Fatalf("LoadCatalog() returned non-zero output on error: %+v", loaded)
	}
}

func TestLoadCatalogLoadsCanonicalFamiliesAndPreservesAdmissions(t *testing.T) {
	root, paths := catalogLoadFixture(t)
	ids := append([]string(nil), approvedFamilyIDs...)
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
	}
	writeLoadFile(t, root, "catalog.json", catalogJSON(ids, paths))
	policy := AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}}
	originalPolicy := append([]string(nil), policy.CompatibleThirdPartyLicenses...)

	loaded, err := LoadCatalog(root, "catalog.json", policy)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Families == nil || loaded.Families == nil || len(loaded.Families) != len(approvedFamilyIDs) {
		t.Fatalf("LoadCatalog() = %+v, want every family", loaded)
	}
	for index, family := range loaded.Families {
		if family.Manifest.ID != approvedFamilyIDs[index] || loaded.Manifest.Families[index].ID != approvedFamilyIDs[index] || family.Capabilities == nil {
			t.Fatalf("family %d = %+v, want canonical order with non-nil capabilities", index, family)
		}
	}
	for index, want := range []struct {
		id       string
		admitted bool
		reason   AdmissionReason
	}{
		{"owned", true, AdmissionReasonCortexOwned},
		{"approved", true, AdmissionReasonThirdPartyLicenseApproved},
		{"rejected", false, AdmissionReasonThirdPartyLicenseRejected},
	} {
		got := loaded.Families[0].Capabilities[index]
		if got.Manifest.ID != want.id || got.Admission.Admitted != want.admitted || got.Admission.Reason != want.reason {
			t.Errorf("capability %d = %+v, want %q/%t/%q", index, got, want.id, want.admitted, want.reason)
		}
	}
	if !reflect.DeepEqual(policy.CompatibleThirdPartyLicenses, originalPolicy) {
		t.Fatal("LoadCatalog() mutated caller policy")
	}
}

func TestLoadCatalogRejectsInvalidRootAndDelegatedFamilyInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		modify func(t *testing.T, root string, paths map[string]string)
	}{
		{"unsafe path", "../catalog.json", nil},
		{"absolute path", "/catalog.json", nil},
		{"wrong suffix", "catalog.md", nil},
		{"missing catalog", "missing.json", nil},
		{"catalog symlink", "catalog.json", func(t *testing.T, root string, _ map[string]string) { replaceWithSymlink(t, root, "catalog.json") }},
		{"catalog directory", "catalog.json", func(t *testing.T, root string, _ map[string]string) { replaceWithDirectory(t, root, "catalog.json") }},
		{"invalid catalog", "catalog.json", func(t *testing.T, root string, _ map[string]string) { writeLoadFile(t, root, "catalog.json", "{") }},
		{"missing family", "catalog.json", func(t *testing.T, root string, paths map[string]string) {
			paths[approvedFamilyIDs[0]] = "missing.json"
			writeLoadFile(t, root, "catalog.json", catalogJSON(approvedFamilyIDs, paths))
		}},
		{"invalid family", "catalog.json", func(t *testing.T, root string, paths map[string]string) {
			writeLoadFile(t, root, paths[approvedFamilyIDs[0]], "{")
		}},
		{"family id mismatch", "catalog.json", func(t *testing.T, root string, paths map[string]string) {
			writeLoadFile(t, root, paths[approvedFamilyIDs[0]], familyJSON(approvedFamilyIDs[1], "routers/x.md", `[]`, `[]`))
			writeLoadFile(t, root, "routers/x.md", "x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, paths := catalogLoadFixture(t)
			if tc.modify != nil {
				tc.modify(t, root, paths)
			}
			catalogLoadError(t, root, tc.path, AdmissionPolicy{})
		})
	}
}

func TestLoadCatalogRejectsInvalidPolicyBeforeCatalogIO(t *testing.T) {
	loaded, err := LoadCatalog("", "missing.json", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT", "MIT"}})
	if err == nil || err.Error() != "catalog load: admission policy is invalid" {
		t.Fatalf("LoadCatalog() invalid policy error = %v", err)
	}
	if loaded.Manifest.SchemaVersion != 0 || loaded.Manifest.Families != nil || loaded.Families != nil {
		t.Fatalf("LoadCatalog() invalid policy output = %+v", loaded)
	}
}

func TestLoadCatalogRejectsDuplicateCapabilitiesAndIgnoresExtras(t *testing.T) {
	root, paths := catalogLoadFixture(t)
	first, second := approvedFamilyIDs[0], approvedFamilyIDs[1]
	writeLoadFile(t, root, paths[first], familyJSON(first, "routers/"+first+".md", `[
		"manifests/duplicate-first.json"]`, `[]`))
	writeLoadFile(t, root, "manifests/duplicate-first.json", loadCapabilityJSON("duplicate", first, "sources/duplicate-first.md", ProvenanceThirdParty, "GPL-3.0-only"))
	writeLoadFile(t, root, "sources/duplicate-first.md", "first")
	writeLoadFile(t, root, paths[second], familyJSON(second, "routers/"+second+".md", `[
		"manifests/duplicate-second.json"]`, `[]`))
	writeLoadFile(t, root, "manifests/duplicate-second.json", loadCapabilityJSON("duplicate", second, "sources/duplicate-second.md", ProvenanceThirdParty, "GPL-3.0-only"))
	writeLoadFile(t, root, "sources/duplicate-second.md", "second")
	catalogLoadError(t, root, "catalog.json", AdmissionPolicy{})

	root, _ = catalogLoadFixture(t)
	writeLoadFile(t, root, "unreferenced/invalid.json", "not a manifest")
	loaded, err := LoadCatalog(root, "catalog.json", AdmissionPolicy{})
	if err != nil || len(loaded.Families) != len(approvedFamilyIDs) {
		t.Fatalf("LoadCatalog() with extra file = (%+v, %v)", loaded, err)
	}
}

func TestLoadCatalogErrorsDoNotLeakSensitiveInput(t *testing.T) {
	root, _ := catalogLoadFixture(t)
	secret := "https://user:password@example.invalid/private-license.json"
	writeLoadFile(t, root, "catalog.json", fmt.Sprintf(`{"schemaVersion":1,"families":{"%s":%q}}`, approvedFamilyIDs[0], secret))
	catalogLoadError(t, root, "catalog.json", AdmissionPolicy{})
	_, err := LoadCatalog(root, "catalog.json", AdmissionPolicy{})
	for _, forbidden := range []string{root, "https", "password", "example.invalid", "private-license"} {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(forbidden)) {
			t.Fatalf("LoadCatalog() leaked %q in %q", forbidden, err)
		}
	}
}
