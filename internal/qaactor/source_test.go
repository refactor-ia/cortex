package qaactor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
)

const (
	syntheticCapabilityID           = "synthetic-operator"
	syntheticCapabilityManifestPath = "families/quality-assurance/capabilities/synthetic-operator.json"
	syntheticCapabilitySourcePath   = "families/quality-assurance/sources/synthetic-operator.md"
)

func TestSourcesExtractsOnlyActorsFromTrailingNonAgentCapability(t *testing.T) {
	snapshot := syntheticCapabilitySnapshot(t, nil)
	sources, err := Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	contracts := qarole.Catalog()
	capabilities := qaFamily(t, snapshot).Capabilities()
	if got, want := len(capabilities), len(contracts)+1; got != want {
		t.Fatalf("QA capability count = %d, want %d", got, want)
	}
	if got := capabilities[len(contracts)].Manifest().ID; got != syntheticCapabilityID {
		t.Fatalf("trailing non-agent capability = %q, want %q", got, syntheticCapabilityID)
	}

	actorSources := sources.Sources()
	if got, want := len(actorSources), len(contracts); got != want {
		t.Fatalf("actor source count = %d, want %d", got, want)
	}
	for index, contract := range contracts {
		if actorSources[index].RoleID() != contract.ID {
			t.Fatalf("actor source %d = %q, want %q", index, actorSources[index].RoleID(), contract.ID)
		}
	}
}

func TestSourcesRejectsActorCapabilityRoleDriftWithTrailingNonAgent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*catalog.FamilyManifest)
	}{
		{
			name: "reordered actors",
			mutate: func(family *catalog.FamilyManifest) {
				family.Capabilities[0], family.Capabilities[1] = family.Capabilities[1], family.Capabilities[0]
			},
		},
		{
			name: "actor displaced after non-agent",
			mutate: func(family *catalog.FamilyManifest) {
				actors := append([]string(nil), family.Capabilities[:len(qarole.Catalog())]...)
				family.Capabilities = append(actors[:len(actors)-1], syntheticCapabilityManifestPath, actors[len(actors)-1])
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := syntheticCapabilitySnapshot(t, tc.mutate)
			if _, err := Sources(snapshot); err == nil {
				t.Fatal("Sources() accepted actor capability role drift")
			}
		})
	}
}

func TestMatchesRolesRejectsReorderedAndDuplicateRoleDrift(t *testing.T) {
	contracts := qarole.Catalog()
	canonical := make([]string, len(contracts))
	for index, contract := range contracts {
		canonical[index] = string(contract.ID)
	}

	for _, tc := range []struct {
		name  string
		roles func([]string)
	}{
		{
			name: "reordered roles",
			roles: func(roles []string) {
				roles[0], roles[1] = roles[1], roles[0]
			},
		},
		{
			name: "duplicate role",
			roles: func(roles []string) {
				roles[1] = roles[0]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles := append([]string(nil), canonical...)
			tc.roles(roles)
			if matchesRoles(roles, contracts) {
				t.Fatal("matchesRoles() accepted role drift")
			}
		})
	}
}

func syntheticCapabilitySnapshot(t *testing.T, mutate func(*catalog.FamilyManifest)) catalog.CatalogSnapshot {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "catalog"))); err != nil {
		t.Fatal(err)
	}

	familyPath := filepath.Join(root, filepath.FromSlash("families/quality-assurance/family.json"))
	familyData, err := os.ReadFile(familyPath)
	if err != nil {
		t.Fatal(err)
	}
	family, err := catalog.DecodeFamilyManifest(familyData)
	if err != nil {
		t.Fatal(err)
	}
	family.Capabilities = append(qaActorCapabilityPaths(), syntheticCapabilityManifestPath)
	if mutate != nil {
		mutate(&family)
	}
	familyData, err = json.Marshal(family)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(familyPath, append(familyData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, filepath.FromSlash(syntheticCapabilityManifestPath))
	if err := os.WriteFile(manifestPath, []byte(`{"schemaVersion":1,"id":"synthetic-operator","description":"Synthetic non-agent QA capability for actor extraction tests.","family":"quality-assurance","source":"families/quality-assurance/sources/synthetic-operator.md","activation":"automatic","provenance":"cortex-owned","license":"CC-BY-SA-4.0","redistributionAllowed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, filepath.FromSlash(syntheticCapabilitySourcePath))
	if err := os.WriteFile(sourcePath, []byte("# Synthetic Operator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return buildSnapshot(t, root)
}

func qaActorCapabilityPaths() []string {
	paths := make([]string, 0, len(qarole.Catalog()))
	for _, contract := range qarole.Catalog() {
		paths = append(paths, "families/quality-assurance/capabilities/"+string(contract.ID)+".json")
	}
	return paths
}
