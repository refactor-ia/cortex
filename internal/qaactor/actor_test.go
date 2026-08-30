package qaactor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestSourcesReturnsCanonicalSourceSet(t *testing.T) {
	snapshot := testSnapshot(t)
	sources, err := Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if sources.CatalogFingerprint() != snapshot.Fingerprint() {
		t.Fatal("catalog fingerprint differs from snapshot")
	}
	capabilities := qaFamily(t, snapshot).Capabilities()
	for index, contract := range qarole.Catalog() {
		source := sources.Sources()[index]
		capability := capabilities[index]
		if source.RoleID() != contract.ID || source.RoleContractVersion() != contract.ContractVersion || source.Description() != capability.Manifest().Description {
			t.Fatalf("source %d does not match its role contract", index)
		}
		if source.SourceSHA256() != capability.Source().SHA256() || string(source.Body()) != string(capability.Source().Content()) {
			t.Fatalf("source %q does not match the catalog file", contract.ID)
		}
		if err := qarole.ValidateSourceContract(contract, string(source.Body())); err != nil {
			t.Fatalf("source %q has invalid markers: %v", contract.ID, err)
		}
	}
}

func TestSourceSetCopiesBodies(t *testing.T) {
	sources, err := Sources(testSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	first := sources.Sources()
	first[0].Body()[0] = 'X'
	if sources.Sources()[0].Body()[0] == 'X' {
		t.Fatal("Sources() returned shared body bytes")
	}
}

func TestSourcesRejectsInvalidCatalogs(t *testing.T) {
	if _, err := Sources(catalog.CatalogSnapshot{}); err == nil {
		t.Fatal("Sources() accepted an empty snapshot")
	}

	for _, tc := range []struct {
		name, path, old, new string
	}{
		{"missing marker", "families/quality-assurance/sources/requirements-analyst.md", "<!-- cortex-qa:role-id=requirements-analyst -->", ""},
		{"reordered roles", "families/quality-assurance/family.json", `"agents": ["requirements-analyst", "test-designer"`, `"agents": ["test-designer", "requirements-analyst"`},
		{"missing role", "families/quality-assurance/family.json", `, "evidence-auditor"]`, "]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := changedSnapshot(t, tc.path, tc.old, tc.new)
			if _, err := Sources(snapshot); err == nil {
				t.Fatal("Sources() accepted an invalid QA catalog")
			}
		})
	}
}

func TestSourcesRetainsChangedCatalogBytes(t *testing.T) {
	snapshot := changedSnapshot(t, "families/quality-assurance/sources/test-runner.md", "# Test Runner", "# Changed Test Runner")
	sources, err := Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sources.Sources()[4].Body()), "# Changed Test Runner") {
		t.Fatal("Sources() did not retain the supplied snapshot bytes")
	}
}

func testSnapshot(t *testing.T) catalog.CatalogSnapshot {
	t.Helper()
	return buildSnapshot(t, filepath.Join("..", "..", "catalog"))
}

func changedSnapshot(t *testing.T, path, old, new string) catalog.CatalogSnapshot {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "catalog"))); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, path)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(data), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	return buildSnapshot(t, root)
}

func buildSnapshot(t *testing.T, root string) catalog.CatalogSnapshot {
	t.Helper()
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func qaFamily(t *testing.T, snapshot catalog.CatalogSnapshot) catalog.CatalogFamilySnapshot {
	t.Helper()
	for _, family := range snapshot.Families() {
		if family.Manifest().ID == "quality-assurance" {
			return family
		}
	}
	t.Fatal("quality-assurance family is missing")
	return catalog.CatalogFamilySnapshot{}
}
