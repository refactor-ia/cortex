package installplan_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

type capability struct{ id, family string }

func TestBuildRuntimeCandidates(t *testing.T) {
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
			resolved := resolve(t, test.runtime, home, []capability{{"alpha", "reasoning"}})
			plan, err := installplan.Build(resolved)
			if err != nil || plan.RuntimeID() != test.runtime || plan.RootKind() != resolved.RootKind() || plan.SnapshotFingerprint() != resolved.SnapshotFingerprint() || plan.RootPath() != test.root {
				t.Fatalf("Build() = (%#v, %v)", plan, err)
			}
			files := plan.Files()
			if len(files) != 2 || files[0].Role() != "skill" || files[0].LogicalID() != "skills/alpha" || files[0].RelativePath() != "skills/cortex-alpha/SKILL.md" || files[0].AbsolutePath() != filepath.Join(test.root, "skills", "cortex-alpha", "SKILL.md") || files[1].Role() != "state" || files[1].LogicalID() != "state/install-state" || files[1].RelativePath() != ".cortex/install-state.json" || files[1].AbsolutePath() != filepath.Join(test.root, ".cortex", "install-state.json") {
				t.Fatalf("Files() = %#v", files)
			}
			assertState(t, plan, files[:1])
		})
	}
}

func TestBuildPreservesProjectionOrderAndBoundaries(t *testing.T) {
	home := t.TempDir()
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			resolved := resolve(t, runtime, home, []capability{{"zeta", "reasoning"}, {"alpha", "services"}})
			plan, err := installplan.Build(resolved)
			if err != nil {
				t.Fatal(err)
			}
			files, targets := plan.Files(), resolved.Targets()
			if len(files) != 3 || files[0].LogicalID() != "skills/alpha" || files[1].LogicalID() != "skills/zeta" || files[2].Role() != "state" {
				t.Fatalf("ordering = %#v", files)
			}
			for index, target := range targets {
				if !bytes.Equal(files[index].Content(), target.Content()) || files[index].SHA256() != target.SHA256() || digest(files[index].Content()) != files[index].SHA256() {
					t.Fatalf("projection %d was not preserved", index)
				}
			}
			assertState(t, plan, files[:2])
		})
	}

	long := strings.Repeat("a", 57)
	plan, err := installplan.Build(resolve(t, runtimematrix.RuntimeOpenCode, home, []capability{{long, "reasoning"}}))
	if err != nil || plan.Files()[0].RelativePath() != "skills/cortex-"+long+"/SKILL.md" || plan.InstalledState().Artifacts()[0].RelativePath() != "skills/cortex-"+long+"/SKILL.md" {
		t.Fatalf("57-character ID = (%#v, %v)", plan, err)
	}
}

func TestBuildStateDeterminismPrivacyAndIsolation(t *testing.T) {
	home := t.TempDir()
	resolved := resolve(t, runtimematrix.RuntimePi, home, []capability{{"alpha", "reasoning"}})
	first, err := installplan.Build(resolved)
	second, secondErr := installplan.Build(resolved)
	if err != nil || secondErr != nil || !bytes.Equal(first.StateJSON(), second.StateJSON()) || !sameFiles(first.Files(), second.Files()) {
		t.Fatalf("Build() was not deterministic: %v, %v", err, secondErr)
	}
	files, state := first.Files(), first.StateJSON()
	files[0] = installplan.File{}
	files[1].Content()[0] ^= 1
	state[0] ^= 1
	if first.Files()[0].LogicalID() != "skills/alpha" || first.Files()[1].Content()[0] != first.StateJSON()[0] || bytes.Equal(state, first.StateJSON()) {
		t.Fatal("Build exposed mutable candidate state")
	}
	for _, private := range []string{home, first.RootPath(), string(resolved.Targets()[0].Content())} {
		if strings.Contains(string(first.StateJSON()), private) {
			t.Fatalf("state JSON leaked private candidate data")
		}
	}
	assertState(t, first, first.Files()[:1])
}

func TestBuildRejectsZeroPlanWithoutLeakage(t *testing.T) {
	plan, err := installplan.Build(skillroot.Plan{})
	if err == nil || err.Error() != "install plan: invalid candidate" || plan.RuntimeID() != "" || plan.RootKind() != "" || plan.SnapshotFingerprint() != "" || plan.RootPath() != "" || plan.Files() == nil || len(plan.Files()) != 0 || plan.StateJSON() == nil || len(plan.StateJSON()) != 0 || strings.Contains(err.Error(), "private-marker") {
		t.Fatalf("Build() = (%#v, %v)", plan, err)
	}
}

func assertState(t *testing.T, plan installplan.Plan, skills []installplan.File) {
	t.Helper()
	manifest := plan.InstalledState()
	decoded, err := installstate.Decode(plan.StateJSON())
	if err != nil || !bytes.Equal(plan.StateJSON(), mustEncode(t, manifest)) || !bytes.Equal(plan.StateJSON(), mustEncode(t, decoded)) || manifest.RuntimeID() != plan.RuntimeID() || manifest.RootKind() != plan.RootKind() || manifest.SnapshotFingerprint() != plan.SnapshotFingerprint() {
		t.Fatalf("state = (%#v, %v)", manifest, err)
	}
	artifacts := manifest.Artifacts()
	if len(artifacts) != len(skills) {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	for index, skill := range skills {
		if artifacts[index].LogicalID() != skill.LogicalID() || artifacts[index].RelativePath() != skill.RelativePath() || artifacts[index].SHA256() != skill.SHA256() || skill.SHA256() != digest(skill.Content()) {
			t.Fatalf("artifact %d = %#v", index, artifacts[index])
		}
	}
	state := plan.Files()[len(skills)]
	if state.SHA256() != digest(plan.StateJSON()) || !bytes.Equal(state.Content(), plan.StateJSON()) {
		t.Fatal("state content or hash differs from canonical state")
	}
}

func mustEncode(t *testing.T, manifest installstate.Manifest) []byte {
	t.Helper()
	data, err := installstate.Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameFiles(left, right []installplan.File) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Role() != right[index].Role() || left[index].LogicalID() != right[index].LogicalID() || left[index].RelativePath() != right[index].RelativePath() || left[index].AbsolutePath() != right[index].AbsolutePath() || left[index].SHA256() != right[index].SHA256() || !bytes.Equal(left[index].Content(), right[index].Content()) {
			return false
		}
	}
	return true
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func resolve(t *testing.T, runtime runtimematrix.RuntimeID, home string, capabilities []capability) skillroot.Plan {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		families[family] = "families/" + family + ".json"
		paths := []string{}
		for _, capability := range capabilities {
			if capability.family == family {
				paths = append(paths, "manifests/"+capability.id+".json")
			}
		}
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": paths, "agents": []string{}})
		writeFile(t, root, "routers/"+family+".md", family)
	}
	for _, capability := range capabilities {
		writeJSON(t, root, "manifests/"+capability.id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: capability.id, Description: capability.id, Family: capability.family, Source: "sources/" + capability.id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		writeFile(t, root, "sources/"+capability.id+".md", capability.id+"\n")
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
	assessments, observations := []projection.Assessment{}, []runtimematrix.Observation{}
	for _, id := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		value, err := skillprojection.Build(id, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments, observations = append(assessments, value.Assessment()), append(observations, runtimematrix.Observation{ID: id, Present: true, Version: "test", Compatibility: runtimematrix.Compatible})
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
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
	symbolic, err := skilldest.Build(binding)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: home})
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
