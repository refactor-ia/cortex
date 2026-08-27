package skillartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildExactArtifactsAndImmutability(t *testing.T) {
	projected, final := skillPipeline(t, false, false, runtimematrix.RuntimeOpenCode)
	binding, err := Build(projected, final)
	manifest, manifestOK := binding.Manifest()
	bundle, bundleOK := binding.Bundle()
	if err != nil || !binding.HasArtifacts() || binding.ReasonCode() != "" || !manifestOK || !bundleOK {
		t.Fatalf("Build() = (%#v, %v)", binding, err)
	}
	skills := projected.Skills()
	want := fmt.Sprintf(`{"schemaVersion":1,"owner":"cortex","snapshotFingerprint":%q,"runtime":"opencode","projectionResult":"exact","artifacts":{"skills/alpha":%q,"skills/zeta":%q}}`, projected.Assessment().SnapshotFingerprint(), skills[0].SHA256(), skills[1].SHA256())
	if string(binding.ManifestJSON()) != want || manifest.Owner() != "cortex" || manifest.RuntimeID() != runtimematrix.RuntimeOpenCode || manifest.TranslationDisclosure() != "" {
		t.Fatal("exact manifest is not canonical")
	}
	artifacts := bundle.Artifacts()
	if len(artifacts) != 2 || artifacts[0].LogicalID() != "skills/alpha" || artifacts[1].LogicalID() != "skills/zeta" || !bytes.Equal(artifacts[0].Content(), skills[0].Content()) || artifacts[0].SHA256() != skills[0].SHA256() {
		t.Fatal("bundle did not bind ordered projected content")
	}
	data := binding.ManifestJSON()
	data[0] = 'x'
	returned := manifest.Artifacts()
	returned[0] = returned[1]
	content := bundle.Artifacts()[0].Content()
	content[0] = 'x'
	artifacts[0] = artifacts[1]
	boundAgain, _ := binding.Bundle()
	manifestAgain, _ := binding.Manifest()
	if binding.ManifestJSON()[0] == 'x' || manifestAgain.Artifacts()[0].LogicalID() != "skills/alpha" || bundle.Artifacts()[0].Content()[0] == 'x' || boundAgain.Artifacts()[0].LogicalID() != "skills/alpha" {
		t.Fatal("binding exposed mutable state")
	}
	second, err := Build(projected, final)
	if err != nil || !bytes.Equal(second.ManifestJSON(), binding.ManifestJSON()) {
		t.Fatal("Build must be deterministic")
	}
}
func TestBuildTranslatedAndUnrepresentableDormant(t *testing.T) {
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode} {
		t.Run(string(runtime), func(t *testing.T) {
			projected, final := skillPipeline(t, true, false, runtime)
			binding, err := Build(projected, final)
			if err != nil || !binding.HasArtifacts() || !strings.Contains(string(binding.ManifestJSON()), `"translationDisclosure":"`+skillprojection.TranslationDisclosure+`"`) {
				t.Fatalf("Build() = (%#v, %v)", binding, err)
			}
			bundle, _ := binding.Bundle()
			for index, artifact := range bundle.Artifacts() {
				if artifact.LogicalID() != projected.Skills()[index].LogicalID() || !bytes.Equal(artifact.Content(), projected.Skills()[index].Content()) {
					t.Fatal("translated bytes were not bound")
				}
			}
		})
	}
	projected, final := skillPipeline(t, true, false, runtimematrix.RuntimeOpenCode)
	binding, err := Build(projected, final)
	if err != nil || binding.HasArtifacts() || binding.ReasonCode() != dormantEnforcementUnavailable || binding.ManifestJSON() == nil || len(binding.ManifestJSON()) != 0 {
		t.Fatalf("Build() = (%#v, %v)", binding, err)
	}
	if _, ok := binding.Manifest(); ok {
		t.Fatal("unrepresentable result has manifest")
	}
	if _, ok := binding.Bundle(); ok {
		t.Fatal("unrepresentable result has bundle")
	}
}
func TestBuildEmptyExactAndRejectsMismatches(t *testing.T) {
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		projected, final := skillPipeline(t, false, true, runtime)
		binding, err := Build(projected, final)
		if err != nil || binding.HasArtifacts() || binding.ReasonCode() != emptyProjection || binding.ManifestJSON() == nil || len(binding.ManifestJSON()) != 0 {
			t.Fatalf("empty %s = (%#v, %v)", runtime, binding, err)
		}
		if _, ok := binding.Manifest(); ok {
			t.Fatal("empty result has manifest")
		}
	}
	projected, _ := skillPipeline(t, true, false, runtimematrix.RuntimePi)
	for _, fingerprint := range []string{projected.Assessment().SnapshotFingerprint(), strings.Repeat("f", 64)} {
		base, _ := adapterplan.Build(fingerprint, compatibleObservations())
		pi, _ := projection.NewAssessment(runtimematrix.RuntimePi, fingerprint, projection.Unrepresentable, "")
		oc, _ := projection.NewAssessment(runtimematrix.RuntimeOpenCode, fingerprint, projection.Unrepresentable, "")
		claude, _ := projection.NewAssessment(runtimematrix.RuntimeClaudeCode, fingerprint, projection.Translated, skillprojection.TranslationDisclosure)
		final, _ := projection.BuildPlan(base, []projection.Assessment{pi, oc, claude})
		if binding, err := Build(projected, final); err == nil || binding.HasArtifacts() || binding.ReasonCode() != "" || len(binding.ManifestJSON()) != 0 || err.Error() != "skill artifact: invalid binding" || strings.Contains(err.Error(), fingerprint) {
			t.Fatalf("mismatch = (%#v, %v)", binding, err)
		}
	}
	observations := compatibleObservations()
	observations[0] = runtimematrix.Observation{ID: runtimematrix.RuntimePi, Compatibility: runtimematrix.CompatibilityUnknown}
	base, _ := adapterplan.Build(projected.Assessment().SnapshotFingerprint(), observations)
	oc, _ := projection.NewAssessment(runtimematrix.RuntimeOpenCode, projected.Assessment().SnapshotFingerprint(), projection.Unrepresentable, "")
	claude, _ := projection.NewAssessment(runtimematrix.RuntimeClaudeCode, projected.Assessment().SnapshotFingerprint(), projection.Translated, skillprojection.TranslationDisclosure)
	final, _ := projection.BuildPlan(base, []projection.Assessment{oc, claude})
	if binding, err := Build(projected, final); err == nil || binding.HasArtifacts() || err.Error() != "skill artifact: invalid binding" {
		t.Fatalf("runtime mismatch = (%#v, %v)", binding, err)
	}
}
func skillPipeline(t *testing.T, dormant, empty bool, runtime runtimematrix.RuntimeID) (skillprojection.Plan, projection.Plan) {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		capabilities := []string{}
		if !empty && family == "reasoning" {
			capabilities = []string{"manifests/zeta.json"}
		}
		if !empty && family == "services" {
			capabilities = []string{"manifests/alpha.json"}
		}
		families[family] = "families/" + family + ".json"
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": capabilities, "agents": []string{}})
		writeFile(t, root, "routers/"+family+".md", family)
	}
	if !empty {
		for _, capability := range []struct{ id, family, activation, content string }{{"zeta", "reasoning", catalog.ActivationAutomatic, "zeta\n"}, {"alpha", "services", map[bool]string{true: catalog.ActivationDormant, false: catalog.ActivationAutomatic}[dormant], "alpha\n"}} {
			writeJSON(t, root, "manifests/"+capability.id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: capability.id, Description: capability.id, Family: capability.family, Source: "sources/" + capability.id + ".md", Activation: capability.activation, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
			writeFile(t, root, "sources/"+capability.id+".md", capability.content)
		}
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
	base, err := adapterplan.Build(snapshot.Fingerprint(), compatibleObservations())
	if err != nil {
		t.Fatal(err)
	}
	final, err := projection.BuildPlan(base, assessments)
	if err != nil {
		t.Fatal(err)
	}
	return projected, final
}
func compatibleObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "test-pi", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "test-opencode", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "test-claude", Compatibility: runtimematrix.Compatible}}
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
