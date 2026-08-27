package skilldest_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

type capability struct{ id, family, activation string }

func TestBuildMapsAutomaticBindingsToUserRoots(t *testing.T) {
	for _, test := range []struct {
		runtime runtimematrix.RuntimeID
		root    string
	}{
		{runtimematrix.RuntimePi, "pi-user-agent"},
		{runtimematrix.RuntimeOpenCode, "opencode-user-config"},
		{runtimematrix.RuntimeClaudeCode, "claude-code-user"},
	} {
		t.Run(string(test.runtime), func(t *testing.T) {
			binding := pipeline(t, test.runtime, []capability{{"a", "reasoning", catalog.ActivationAutomatic}})
			plan, err := skilldest.Build(binding)
			if err != nil || plan.RuntimeID() != test.runtime || string(plan.RootKind()) != test.root || plan.SnapshotFingerprint() == "" {
				t.Fatalf("Build() = (%#v, %v)", plan, err)
			}
			destinations := plan.Destinations()
			if len(destinations) != 1 || destinations[0].LogicalID() != "skills/a" || destinations[0].RelativePath() != "skills/cortex-a/SKILL.md" {
				t.Fatalf("Destinations() = %#v", destinations)
			}
		})
	}
}

func TestBuildPreservesTranslatedBytesAndSorts(t *testing.T) {
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			binding := pipeline(t, runtime, []capability{{"z", "reasoning", catalog.ActivationAutomatic}, {"a", "services", catalog.ActivationDormant}})
			plan, err := skilldest.Build(binding)
			bundle, _ := binding.Bundle()
			if err != nil || plan.Destinations()[0].RelativePath() != "skills/cortex-a/SKILL.md" || !bytes.Equal(plan.Destinations()[0].Content(), bundle.Artifacts()[0].Content()) || plan.Destinations()[0].SHA256() != bundle.Artifacts()[0].SHA256() {
				t.Fatalf("translated Build() = (%#v, %v)", plan, err)
			}
		})
	}
	binding := pipeline(t, runtimematrix.RuntimePi, []capability{{"z", "reasoning", catalog.ActivationAutomatic}, {"a", "services", catalog.ActivationAutomatic}})
	plan, err := skilldest.Build(binding)
	if err != nil || plan.Destinations()[0].LogicalID() != "skills/a" || plan.Destinations()[1].LogicalID() != "skills/z" {
		t.Fatalf("sorted Build() = (%#v, %v)", plan, err)
	}
}

func TestBuildBoundaryAndIsolation(t *testing.T) {
	id := strings.Repeat("a", 57)
	binding := pipeline(t, runtimematrix.RuntimeOpenCode, []capability{{id, "reasoning", catalog.ActivationAutomatic}})
	first, err := skilldest.Build(binding)
	second, secondErr := skilldest.Build(binding)
	if err != nil || secondErr != nil || first.SnapshotFingerprint() != second.SnapshotFingerprint() || first.Destinations()[0].RelativePath() != "skills/cortex-"+id+"/SKILL.md" || len("cortex-"+id) != 64 || first.Destinations()[0].LogicalID() != "skills/"+id {
		t.Fatalf("boundary Build() = (%#v, %v)", first, err)
	}
	destinations := first.Destinations()
	content := destinations[0].Content()
	destinations[0] = skilldest.Destination{}
	content[0] ^= 1
	if first.Destinations()[0].LogicalID() != "skills/"+id || bytes.Equal(content, first.Destinations()[0].Content()) {
		t.Fatal("Build exposed mutable destination state")
	}
}

func TestBuildRejectsNoArtifactBindingsWithoutDisclosure(t *testing.T) {
	unrepresentable := pipeline(t, runtimematrix.RuntimeOpenCode, []capability{{"a", "services", catalog.ActivationDormant}})
	bindings := []skillartifact.Binding{{}, unrepresentable}
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		bindings = append(bindings, pipeline(t, runtime, nil))
	}
	for _, binding := range bindings {
		plan, err := skilldest.Build(binding)
		if err == nil || plan.RuntimeID() != "" || plan.RootKind() != "" || len(plan.Destinations()) != 0 || (binding.ReasonCode() != "" && strings.Contains(err.Error(), binding.ReasonCode())) {
			t.Fatalf("Build() = (%#v, %v)", plan, err)
		}
	}
}

func pipeline(t *testing.T, runtime runtimematrix.RuntimeID, capabilities []capability) skillartifact.Binding {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		families[family] = "families/" + family + ".json"
		paths := []string{}
		for _, item := range capabilities {
			if item.family == family {
				paths = append(paths, "manifests/"+item.id+".json")
			}
		}
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": paths, "agents": []string{}})
		writeFile(t, root, "routers/"+family+".md", family)
	}
	for _, item := range capabilities {
		writeJSON(t, root, "manifests/"+item.id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: item.id, Description: item.id, Family: item.family, Source: "sources/" + item.id + ".md", Activation: item.activation, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		writeFile(t, root, "sources/"+item.id+".md", item.id+"\n")
	}
	writeJSON(t, root, "catalog.json", map[string]any{"schemaVersion": 1, "families": families})
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	projected, err := skillprojection.Build(runtime, sources)
	if err != nil {
		t.Fatal(err)
	}
	assessments := make([]projection.Assessment, 0, 3)
	for _, id := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		plan, err := skillprojection.Build(id, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments = append(assessments, plan.Assessment())
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}})
	if err != nil {
		t.Fatal(err)
	}
	final, err := projection.BuildPlan(base, assessments)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := skillartifact.Build(projected, final)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func writeJSON(t *testing.T, root, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, path, string(data))
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
