package skilldest

import (
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

func TestProductionCatalogProjectsExactlyTheQAFleet(t *testing.T) {
	snapshot, err := catalog.BuildCatalogSnapshot("../../catalog", "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatalf("BuildCatalogSnapshot() error = %v", err)
	}
	if got, want := len(snapshot.Families()), len(catalog.ApprovedFamilyIDs()); got != want {
		t.Fatalf("materialized families = %d, want %d", got, want)
	}

	stubCount := 0
	for _, family := range snapshot.Families() {
		manifest := family.Manifest()
		if manifest.ID == "quality-assurance" {
			if got, want := len(manifest.Capabilities), len(qarole.Catalog())+1; got != want {
				t.Fatalf("QA capabilities = %d, want %d", got, want)
			}
			if got, want := len(manifest.Agents), len(qarole.Catalog()); got != want {
				t.Fatalf("QA agents = %d, want %d", got, want)
			}
			continue
		}
		if manifest.ID == "reasoning" {
			if len(manifest.Capabilities) != 1 || len(manifest.Agents) != 0 {
				t.Fatalf("reasoning capabilities = %#v, agents %#v, want one non-agent skill", manifest.Capabilities, manifest.Agents)
			}
			continue
		}
		stubCount++
		if len(manifest.Capabilities) != 0 || len(manifest.Agents) != 0 {
			t.Fatalf("stub family %q = capabilities %#v, agents %#v", manifest.ID, manifest.Capabilities, manifest.Agents)
		}
	}
	if stubCount != 9 {
		t.Fatalf("empty stub families = %d, want 9", stubCount)
	}

	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			sources, binding, destinations := qaPipeline(t, snapshot, runtime)
			ownership, err := ValidateQAProjection(snapshot, sources, binding, destinations)
			if err != nil || len(ownership) != len(qarole.Catalog())+1 {
				t.Fatalf("ValidateQAProjection() = (%#v, %v)", ownership, err)
			}
			for index, contract := range qarole.Catalog() {
				if ownership[index].RoleID != string(contract.ID) {
					t.Fatalf("projected role %d = %q, want %q", index, ownership[index].RoleID, contract.ID)
				}
			}
			if ownership[len(qarole.Catalog())].RoleID != qaNoCICapability {
				t.Fatalf("projected non-agent skill = %q, want %q", ownership[len(qarole.Catalog())].RoleID, qaNoCICapability)
			}
		})
	}
}
