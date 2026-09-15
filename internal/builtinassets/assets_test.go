package builtinassets

import (
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

const builtInFingerprint = "6f08ee25dc84c7cba2be78deab7eeaca8585d5fa1528795a9256e642854fac88"

func TestSnapshotLoadsEmbeddedCatalog(t *testing.T) {
	snapshot, err := Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Manifest().SchemaVersion != 1 || len(snapshot.Families()) != 11 || snapshot.Fingerprint() != builtInFingerprint {
		t.Fatalf("Snapshot() = schema %d, families %d, fingerprint %q", snapshot.Manifest().SchemaVersion, len(snapshot.Families()), snapshot.Fingerprint())
	}
	families := snapshot.Families()
	if len(families[10].Capabilities()) != 1 || families[10].Capabilities()[0].Manifest().ID != "catalog-marker" || string(families[10].Capabilities()[0].Source().Content()) != "# Cortex Catalog Marker\n\nThis skill identifies the built-in Cortex lifecycle catalog. It defines no executable workflow and grants no permission to inspect data, invoke tools, or modify state.\n" {
		t.Fatal("Snapshot() does not materialize the catalog marker")
	}
}

func TestSnapshotProjectsCatalogMarkerToRuntimeDestinations(t *testing.T) {
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
		if err != nil || len(destinations.Destinations()) != 1 || destinations.Destinations()[0].RelativePath() != "skills/cortex-catalog-marker/SKILL.md" {
			t.Fatalf("runtime destination = (%+v, %v)", destinations, err)
		}
	}
}
