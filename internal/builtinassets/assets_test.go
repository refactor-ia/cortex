package builtinassets

import (
	"slices"
	"sort"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

const builtInFingerprint = "6e182caced3a6d0432b00cd89365fef4aa287b74c4ef83a9e1dfb2983342b722"

func TestSnapshotLoadsEmbeddedCatalog(t *testing.T) {
	snapshot, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Manifest().SchemaVersion != 1 || len(snapshot.Families()) != 11 || snapshot.Fingerprint() != builtInFingerprint {
		t.Fatalf("Snapshot() = schema %d, families %d, fingerprint %q", snapshot.Manifest().SchemaVersion, len(snapshot.Families()), snapshot.Fingerprint())
	}
	qualityAssurance := familyByID(t, snapshot, "quality-assurance")
	if len(qualityAssurance.Capabilities()) != 6 {
		t.Fatalf("quality-assurance capabilities = %d, want 6", len(qualityAssurance.Capabilities()))
	}
	if total := totalCapabilities(snapshot); total != 6 {
		t.Fatalf("embedded capabilities = %d, want 6", total)
	}
}

func familyByID(t *testing.T, snapshot catalog.CatalogSnapshot, id string) catalog.CatalogFamilySnapshot {
	t.Helper()
	for _, family := range snapshot.Families() {
		if family.Manifest().ID == id {
			return family
		}
	}
	t.Fatalf("family %q is missing from the embedded catalog", id)
	return catalog.CatalogFamilySnapshot{}
}

func totalCapabilities(snapshot catalog.CatalogSnapshot) int {
	total := 0
	for _, family := range snapshot.Families() {
		total += len(family.Capabilities())
	}
	return total
}

func TestSnapshotProjectsEmbeddedCapabilitiesToRuntimeDestinations(t *testing.T) {
	snapshot, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sources, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ids := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	assessments, projected := make([]projection.Assessment, 0, len(ids)), make([]skillprojection.Plan, 0, len(ids))
	for _, id := range ids {
		plan, err := skillprojection.Build(id, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments, projected = append(assessments, plan.Assessment()), append(projected, plan)
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}})
	if err != nil {
		t.Fatal(err)
	}
	final, err := projection.BuildPlan(base, assessments)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range projected {
		binding, err := skillartifact.Build(plan, final)
		if err != nil {
			t.Fatal(err)
		}
		destinations, err := skilldest.Build(binding)
		if err != nil {
			t.Fatal(err)
		}
		paths := make([]string, 0, len(destinations.Destinations()))
		for _, destination := range destinations.Destinations() {
			paths = append(paths, destination.RelativePath())
		}
		sort.Strings(paths)
		if !slices.Equal(paths, expectedDestinations) {
			t.Fatalf("runtime destinations = %v, want %v", paths, expectedDestinations)
		}
	}
}

// expectedDestinations is one skill per embedded capability: the six
// quality-assurance capabilities, the only capabilities the catalog carries.
var expectedDestinations = []string{
	"skills/cortex-adversarial-tester/SKILL.md",
	"skills/cortex-evidence-auditor/SKILL.md",
	"skills/cortex-exploratory-tester/SKILL.md",
	"skills/cortex-requirements-analyst/SKILL.md",
	"skills/cortex-test-designer/SKILL.md",
	"skills/cortex-test-runner/SKILL.md",
}
