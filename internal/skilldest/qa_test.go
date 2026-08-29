package skilldest

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
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

func TestValidateQAProjectionBindsSixNeutralSourcesToSupportedDestinations(t *testing.T) {
	for _, runtime := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode} {
		t.Run(string(runtime), func(t *testing.T) {
			snapshot := qaSnapshot(t, false, false)
			sources, binding, destinations := qaPipeline(t, snapshot, runtime)
			ownership, err := ValidateQAProjection(snapshot, sources, binding, destinations)
			if err != nil || len(ownership) != len(qarole.Catalog()) {
				t.Fatalf("ValidateQAProjection() = (%#v, %v)", ownership, err)
			}
			sourceHashes := qaSourceHashes(snapshot)
			for _, record := range ownership {
				if record.SnapshotFingerprint != snapshot.Fingerprint() || record.RuntimeID != runtime || record.SourceSHA256 != sourceHashes[record.RoleID] || record.GeneratedSHA256 == "" || record.Destination != "skills/cortex-"+record.RoleID+"/SKILL.md" {
					t.Fatalf("ownership record = %#v", record)
				}
			}
		})
	}
}

func TestValidateQAProjectionRejectsDivergentDestinationOwnership(t *testing.T) {
	snapshot := qaSnapshot(t, false, false)
	sources, binding, destinations := qaPipeline(t, snapshot, runtimematrix.RuntimePi)
	cases := []struct {
		name string
		edit func(*Plan)
	}{
		{"omitted required marker", func(plan *Plan) {
			plan.destinations[0].content = bytes.Replace(plan.destinations[0].content, []byte("<!-- cortex-qa:role-id="), []byte("<!-- cortex-qa:removed-role-id="), 1)
		}},
		{"changed required marker", func(plan *Plan) {
			plan.destinations[0].content = bytes.Replace(plan.destinations[0].content, []byte("evidence-only-output"), []byte("integrated-fix"), 1)
		}},
		{"generated hash mismatch", func(plan *Plan) { plan.destinations[0].sha256 = strings.Repeat("0", 64) }},
		{"payload artifact divergence", func(plan *Plan) { plan.destinations[0].content = append(plan.destinations[0].content, '!') }},
		{"wrong destination", func(plan *Plan) { plan.destinations[0].relativePath = "skills/cortex-wrong/SKILL.md" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := destinations
			candidate.destinations = append([]Destination(nil), destinations.destinations...)
			tc.edit(&candidate)
			if ownership, err := ValidateQAProjection(snapshot, sources, binding, candidate); err == nil || ownership != nil {
				t.Fatalf("ValidateQAProjection() = (%#v, %v)", ownership, err)
			}
		})
	}
}

func TestValidateQAProjectionRejectsDeferredSpecialistsAndPreservesNonQAPlanning(t *testing.T) {
	snapshot := qaSnapshot(t, true, true)
	sources, binding, destinations := qaPipeline(t, snapshot, runtimematrix.RuntimeOpenCode)
	if ownership, err := ValidateQAProjection(snapshot, sources, binding, destinations); err == nil || ownership != nil {
		t.Fatalf("deferred specialist result = (%#v, %v)", ownership, err)
	}

	roleMismatch := qaSnapshot(t, false, false, true)
	sources, binding, destinations = qaPipeline(t, roleMismatch, runtimematrix.RuntimePi)
	if ownership, err := ValidateQAProjection(roleMismatch, sources, binding, destinations); err == nil || ownership != nil {
		t.Fatalf("role mismatch result = (%#v, %v)", ownership, err)
	}

	markerMutation := qaSnapshot(t, false, false, false, true)
	sources, binding, destinations = qaPipeline(t, markerMutation, runtimematrix.RuntimePi)
	if ownership, err := ValidateQAProjection(markerMutation, sources, binding, destinations); err == nil || ownership != nil {
		t.Fatalf("neutral-source marker mutation result = (%#v, %v)", ownership, err)
	}

	nonQASnapshot := qaSnapshot(t, false, true)
	nonQASources, nonQABinding, nonQADestinations := qaPipeline(t, nonQASnapshot, runtimematrix.RuntimeOpenCode)
	ownership, err := ValidateQAProjection(nonQASnapshot, nonQASources, nonQABinding, nonQADestinations)
	containsAlpha := false
	for _, destination := range nonQADestinations.Destinations() {
		containsAlpha = containsAlpha || destination.LogicalID() == "skills/alpha"
	}
	if err != nil || len(ownership) != 6 || len(nonQADestinations.Destinations()) != 7 || !containsAlpha {
		t.Fatalf("non-QA planning result = (%#v, %#v, %v)", ownership, nonQADestinations, err)
	}
}

func TestValidateQAProjectionRejectsSnapshotAndRuntimeMismatches(t *testing.T) {
	snapshot := qaSnapshot(t, false, false)
	sources, binding, destinations := qaPipeline(t, snapshot, runtimematrix.RuntimePi)
	if ownership, err := ValidateQAProjection(qaSnapshot(t, false, true), sources, binding, destinations); err == nil || ownership != nil {
		t.Fatalf("snapshot mismatch result = (%#v, %v)", ownership, err)
	}

	runtimeMismatch := destinations
	runtimeMismatch.runtimeID = runtimematrix.RuntimeOpenCode
	if ownership, err := ValidateQAProjection(snapshot, sources, binding, runtimeMismatch); err == nil || ownership != nil {
		t.Fatalf("runtime mismatch result = (%#v, %v)", ownership, err)
	}

	claudeSources, claudeBinding, claudeDestinations := qaPipeline(t, snapshot, runtimematrix.RuntimeClaudeCode)
	if ownership, err := ValidateQAProjection(snapshot, claudeSources, claudeBinding, claudeDestinations); err == nil || ownership != nil {
		t.Fatalf("unsupported Claude Code result = (%#v, %v)", ownership, err)
	}
}

func TestQAFamilyMismatchStopsAtCatalogAdmission(t *testing.T) {
	root := t.TempDir()
	qaWrite(t, root, "family.json", jsonValue(t, map[string]any{"schemaVersion": 1, "id": "quality-assurance", "router": "router.md", "capabilities": []string{"capability.json"}, "agents": []string{"requirements-analyst"}}))
	qaWrite(t, root, "router.md", "# QA\n")
	qaWrite(t, root, "capability.json", manifestJSON(t, "requirements-analyst", "reasoning"))
	qaWrite(t, root, "sources/requirements-analyst.md", qaSource(qarole.Catalog()[0]))
	if _, err := catalog.LoadFamily(root, "family.json", catalog.AdmissionPolicy{}); err == nil {
		t.Fatal("LoadFamily accepted a cross-family capability")
	}
}

func qaPipeline(t *testing.T, snapshot catalog.CatalogSnapshot, runtime runtimematrix.RuntimeID) (skillrender.Set, skillartifact.Binding, Plan) {
	t.Helper()
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
	base, err := adapterplan.Build(snapshot.Fingerprint(), qaObservations())
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
	destinations, err := Build(binding)
	if err != nil {
		t.Fatal(err)
	}
	return sources, binding, destinations
}

func qaSnapshot(t *testing.T, deferred, nonQA bool, roleMismatch ...bool) catalog.CatalogSnapshot {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, id := range catalog.ApprovedFamilyIDs() {
		families[id] = "families/" + id + "/family.json"
		capabilities, agents := []string{}, []string{}
		if id == "quality-assurance" {
			for _, contract := range qarole.Catalog() {
				capabilities, agents = append(capabilities, "families/quality-assurance/capabilities/"+string(contract.ID)+".json"), append(agents, string(contract.ID))
				if len(roleMismatch) > 0 && roleMismatch[0] && contract.ID == "evidence-auditor" {
					agents[len(agents)-1] = "security-audit"
				}
				qaWrite(t, root, "families/quality-assurance/capabilities/"+string(contract.ID)+".json", manifestJSON(t, string(contract.ID), id))
				source := qaSource(contract)
				if len(roleMismatch) > 1 && roleMismatch[1] && contract.ID == "requirements-analyst" {
					source = strings.Replace(source, "evidence-only-output", "integrated-fix", 1)
				}
				qaWrite(t, root, "families/quality-assurance/sources/"+string(contract.ID)+".md", source)
			}
			if deferred {
				capabilities, agents = append(capabilities, "families/quality-assurance/capabilities/security-audit.json"), append(agents, "security-audit")
				qaWrite(t, root, "families/quality-assurance/capabilities/security-audit.json", manifestJSON(t, "security-audit", id))
				qaWrite(t, root, "families/quality-assurance/sources/security-audit.md", "# Deferred\n")
			}
		}
		if id == "reasoning" && nonQA {
			capabilities = append(capabilities, "families/reasoning/capabilities/alpha.json")
			qaWrite(t, root, "families/reasoning/capabilities/alpha.json", manifestJSON(t, "alpha", id))
			qaWrite(t, root, "families/reasoning/sources/alpha.md", "# Alpha\n")
		}
		qaWrite(t, root, families[id], jsonValue(t, map[string]any{"schemaVersion": 1, "id": id, "router": "families/" + id + "/router.md", "capabilities": capabilities, "agents": agents}))
		qaWrite(t, root, "families/"+id+"/router.md", "# "+id+"\n")
	}
	qaWrite(t, root, "catalog.json", jsonValue(t, map[string]any{"schemaVersion": 1, "families": families}))
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func qaSourceHashes(snapshot catalog.CatalogSnapshot) map[string]string {
	hashes := map[string]string{}
	for _, family := range snapshot.Families() {
		if family.Manifest().ID == "quality-assurance" {
			for _, capability := range family.Capabilities() {
				hashes[capability.Manifest().ID] = capability.Source().SHA256()
			}
		}
	}
	return hashes
}

func qaSource(contract qarole.RoleContract) string {
	markers := []string{"role-id=" + string(contract.ID), "role-criteria=" + criteria(contract.Criteria)}
	for _, statement := range contract.RequiredStatements {
		markers = append(markers, string(statement))
	}
	markers = append(markers, "diagnostic-mutation-confirmed-disposable-worktree")
	for index, marker := range markers {
		markers[index] = "<!-- cortex-qa:" + marker + " -->"
	}
	return strings.Join(markers, "\n") + "\n"
}

func criteria(values []qarole.Criterion) string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return strings.Join(result, ",")
}

func manifestJSON(t *testing.T, id, family string) string {
	t.Helper()
	return jsonValue(t, catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: id, Family: family, Source: "families/" + family + "/sources/" + id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
}

func jsonValue(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func qaWrite(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func qaObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "test", Compatibility: runtimematrix.Compatible}}
}
