package skillroot_test

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
	"github.com/refactor-ia/cortex/internal/skillroot"
)

type capability struct{ id, family string }

func TestResolveDefaultRoots(t *testing.T) {
	home := t.TempDir()
	for _, test := range []struct {
		runtime runtimematrix.RuntimeID
		root    string
	}{
		{runtimematrix.RuntimePi, filepath.Join(home, ".pi", "agent")},
		{runtimematrix.RuntimeOpenCode, filepath.Join(home, ".config", "opencode")},
		{runtimematrix.RuntimeClaudeCode, filepath.Join(home, ".claude")},
	} {
		t.Run(string(test.runtime), func(t *testing.T) {
			plan, err := skillroot.Resolve(binding(t, test.runtime, []capability{{"a", "reasoning"}}), skillroot.Inputs{Home: home})
			if err != nil || plan.RootPath() != test.root || plan.RuntimeID() != test.runtime {
				t.Fatalf("Resolve() = (%#v, %v)", plan, err)
			}
			if targets := plan.Targets(); len(targets) != 1 || targets[0].AbsolutePath() != filepath.Join(test.root, "skills", "cortex-a", "SKILL.md") {
				t.Fatalf("Targets() = %#v", targets)
			}
		})
	}
}

func TestResolveOverridesAndPreservation(t *testing.T) {
	home, piRoot, claudeRoot := t.TempDir(), t.TempDir(), t.TempDir()
	piPlan := binding(t, runtimematrix.RuntimePi, []capability{{"z", "reasoning"}, {"a", "services"}})
	for _, override := range []string{piRoot, "~", "~" + string(filepath.Separator) + "custom"} {
		plan, err := skillroot.Resolve(piPlan, skillroot.Inputs{Home: home, PiCodingAgentDir: override})
		want := override
		if override == "~" {
			want = home
		}
		if strings.HasPrefix(override, "~"+string(filepath.Separator)) {
			want = filepath.Join(home, override[2:])
		}
		if err != nil || plan.RootPath() != want {
			t.Fatalf("Pi override %q = (%#v, %v)", override, plan, err)
		}
	}
	claudePlan := binding(t, runtimematrix.RuntimeClaudeCode, []capability{{"a", "reasoning"}})
	plan, err := skillroot.Resolve(claudePlan, skillroot.Inputs{Home: home, ClaudeConfigDir: claudeRoot})
	if err != nil || plan.RootPath() != claudeRoot {
		t.Fatalf("Claude override = (%#v, %v)", plan, err)
	}
	opencode, err := skillroot.Resolve(binding(t, runtimematrix.RuntimeOpenCode, []capability{{strings.Repeat("a", 57), "reasoning"}}), skillroot.Inputs{Home: home})
	if err != nil || opencode.RootPath() != filepath.Join(home, ".config", "opencode") || len(opencode.Targets()) != 1 || opencode.Targets()[0].AbsolutePath() != filepath.Join(home, ".config", "opencode", "skills", "cortex-"+strings.Repeat("a", 57), "SKILL.md") {
		t.Fatalf("OpenCode default = (%#v, %v)", opencode, err)
	}
	targets := plan.Targets()
	piResolved, err := skillroot.Resolve(piPlan, skillroot.Inputs{Home: home})
	if err != nil || targets[0].LogicalID() != "skills/a" || !bytes.Equal(targets[0].Content(), claudePlan.Destinations()[0].Content()) || targets[0].SHA256() != claudePlan.Destinations()[0].SHA256() || !bytes.Equal(piResolved.Targets()[0].Content(), piPlan.Destinations()[0].Content()) || piResolved.Targets()[0].SHA256() != piPlan.Destinations()[0].SHA256() || piResolved.Targets()[0].LogicalID() != "skills/a" || piResolved.Targets()[1].LogicalID() != "skills/z" {
		t.Fatal("resolution did not preserve translated sorted targets")
	}
}

func TestResolveRejectsUnsafeInputsWithoutLeaks(t *testing.T) {
	home, plan := t.TempDir(), binding(t, runtimematrix.RuntimePi, []capability{{"a", "reasoning"}})
	claudePlan := binding(t, runtimematrix.RuntimeClaudeCode, []capability{{"a", "reasoning"}})
	inputs := []skillroot.Inputs{
		{}, {Home: "relative"}, {Home: home + string(filepath.Separator) + ".."}, {Home: string(filepath.Separator)},
		{Home: home + "\x00private-marker"}, {Home: home, PiCodingAgentDir: "relative"}, {Home: home, PiCodingAgentDir: "~user"},
		{Home: home, PiCodingAgentDir: home + string(filepath.Separator) + ".."}, {Home: home, PiCodingAgentDir: home + "\x00private-marker"},
		{Home: home, PiCodingAgentDir: "~" + string(filepath.Separator) + "."}, {Home: home, ClaudeConfigDir: "~/private-marker"}, {Home: home, ClaudeConfigDir: "relative"},
	}
	for index, input := range inputs {
		symbolic := plan
		if index >= 10 {
			symbolic = claudePlan
		}
		got, err := skillroot.Resolve(symbolic, input)
		if err == nil || got.RuntimeID() != "" || got.RootPath() != "" || len(got.Targets()) != 0 || strings.Contains(err.Error(), "private-marker") || err.Error() != "skill root: invalid plan" {
			t.Fatalf("unsafe input %d = (%#v, %v)", index, got, err)
		}
	}
	if got, err := skillroot.Resolve(skilldest.Plan{}, skillroot.Inputs{Home: home}); err == nil || got.RootPath() != "" || err.Error() != "skill root: invalid plan" {
		t.Fatalf("zero plan = (%#v, %v)", got, err)
	}
}

func TestResolveIsDeterministicAndDetached(t *testing.T) {
	home, source := t.TempDir(), binding(t, runtimematrix.RuntimePi, []capability{{"a", "reasoning"}})
	first, err := skillroot.Resolve(source, skillroot.Inputs{Home: home})
	second, secondErr := skillroot.Resolve(source, skillroot.Inputs{Home: home})
	if err != nil || secondErr != nil || first.RootPath() != second.RootPath() || first.SnapshotFingerprint() != second.SnapshotFingerprint() {
		t.Fatal("resolution was not deterministic")
	}
	targets := first.Targets()
	content := targets[0].Content()
	targets[0] = skillroot.Target{}
	content[0] ^= 1
	if first.Targets()[0].LogicalID() != "skills/a" || bytes.Equal(content, first.Targets()[0].Content()) {
		t.Fatal("resolved plan exposed mutable state")
	}
}

func binding(t *testing.T, runtime runtimematrix.RuntimeID, capabilities []capability) skilldest.Plan {
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
		writeJSON(t, root, "manifests/"+item.id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: item.id, Description: item.id, Family: item.family, Source: "sources/" + item.id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
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
	observations := []runtimematrix.Observation{}
	for _, id := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		value, err := skillprojection.Build(id, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments = append(assessments, value.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: id, Present: true, Version: "test", Compatibility: runtimematrix.Compatible})
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
	if err != nil {
		t.Fatal(err)
	}
	final, err := projection.BuildPlan(base, assessments)
	if err != nil {
		t.Fatal(err)
	}
	result, err := skillartifact.Build(projected, final)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := skilldest.Build(result)
	if err != nil {
		t.Fatal(err)
	}
	return plan
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
