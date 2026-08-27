package installobserve_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

func TestClassifyOwnershipLifecycle(t *testing.T) {
	old, oldBundle := makeCandidate(t, "one", "alpha", "beta")
	fresh, freshBundle := makeCandidate(t, "two", "alpha", "beta")
	current, currentBundle := makeCandidate(t, "one", "alpha")
	oldHash := hashes(old)
	for _, tc := range []struct {
		name      string
		candidate installplan.Plan
		bundle    artifact.Bundle
		prior     *installobserve.PriorState
		slots     []installobserve.SlotObservation
		state     ownership.Action
		ready     bool
		actions   map[string]ownership.Action
	}{
		{"first create", current, currentBundle, nil, absent(current), ownership.Create, true, map[string]ownership.Action{"skills/alpha": ownership.Create}},
		{"no-state collision", current, currentBundle, nil, []installobserve.SlotObservation{{LogicalID: "skills/alpha", Present: true, SHA256: hash("other")}}, ownership.Create, false, map[string]ownership.Action{"skills/alpha": ownership.Conflict}},
		{"prior unchanged", old, oldBundle, prior(old), present(old, oldHash), ownership.Unchanged, true, map[string]ownership.Action{"skills/alpha": ownership.Unchanged, "skills/beta": ownership.Unchanged}},
		{"update replace", fresh, freshBundle, prior(old), present(old, oldHash), ownership.Replace, true, map[string]ownership.Action{"skills/alpha": ownership.Replace, "skills/beta": ownership.Replace}},
		{"obsolete remove", current, currentBundle, prior(old), present(old, oldHash), ownership.Replace, true, map[string]ownership.Action{"skills/alpha": ownership.Unchanged, "skills/beta": ownership.Remove}},
		{"desired drift conflict", fresh, freshBundle, prior(old), []installobserve.SlotObservation{{LogicalID: "skills/alpha", Present: true, SHA256: hash("drift")}, {LogicalID: "skills/beta", Present: true, SHA256: oldHash["skills/beta"]}}, ownership.Replace, false, map[string]ownership.Action{"skills/alpha": ownership.Conflict, "skills/beta": ownership.Replace}},
		{"obsolete drift preserve", current, currentBundle, prior(old), []installobserve.SlotObservation{{LogicalID: "skills/alpha", Present: true, SHA256: oldHash["skills/alpha"]}, {LogicalID: "skills/beta", Present: true, SHA256: hash("drift")}}, ownership.Replace, true, map[string]ownership.Action{"skills/alpha": ownership.Unchanged, "skills/beta": ownership.Preserve}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := installobserve.Classify(tc.candidate, tc.prior, tc.slots)
			if err != nil || got.StateAction() != tc.state {
				t.Fatalf("Classify() = (%#v, %v)", got, err)
			}
			plan, err := ownership.Build(tc.bundle, got.Observed())
			if err != nil || plan.Ready() != tc.ready || actions(plan.Decisions()) == nil || !reflect.DeepEqual(actions(plan.Decisions()), tc.actions) {
				t.Fatalf("ownership.Build() = (%#v, %v)", plan, err)
			}
		})
	}
}

func TestPlanRetainsTrustedBundleWithoutExposingMutableState(t *testing.T) {
	candidate, expected := makeCandidate(t, "one", "alpha")
	bundle, found := candidate.Bundle()
	if !found || !reflect.DeepEqual(bundle.Manifest(), expected.Manifest()) {
		t.Fatalf("Bundle() = (%#v, %t)", bundle, found)
	}
	content := bundle.Artifacts()[0].Content()
	content[0] ^= 1
	again, found := candidate.Bundle()
	if !found || bytes.Equal(content, again.Artifacts()[0].Content()) {
		t.Fatal("plan exposed mutable trusted bundle content")
	}
	if _, err := ownership.Build(again, nil); err != nil {
		t.Fatalf("ownership.Build() = %v", err)
	}
}

func TestClassifyRejectsInvalidInputsAndDetachesResults(t *testing.T) {
	candidate, bundle := makeCandidate(t, "one", "alpha")
	valid := present(candidate, hashes(candidate))
	multi, _ := makeCandidate(t, "one", "alpha", "beta")
	multiHash := hashes(multi)
	for _, tc := range []struct {
		name      string
		candidate installplan.Plan
		prior     *installobserve.PriorState
		slots     []installobserve.SlotObservation
	}{
		{"zero candidate", installplan.Plan{}, nil, valid},
		{"prior state hash", candidate, &installobserve.PriorState{Manifest: candidate.InstalledState(), StateSHA256: hash("wrong")}, valid},
		{"missing slot", candidate, prior(candidate), nil},
		{"invalid absent slot", candidate, prior(candidate), []installobserve.SlotObservation{{LogicalID: "skills/alpha", SHA256: hash("wrong")}}},
		{"unsorted union", multi, prior(multi), []installobserve.SlotObservation{{LogicalID: "skills/beta", Present: true, SHA256: multiHash["skills/beta"]}, {LogicalID: "skills/alpha", Present: true, SHA256: multiHash["skills/alpha"]}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := installobserve.Classify(tc.candidate, tc.prior, tc.slots)
			if err == nil || got.StateAction() != "" || len(got.Observed()) != 0 || err.Error() != "install observe: invalid input" {
				t.Fatalf("Classify() = (%#v, %v)", got, err)
			}
		})
	}
	got, err := installobserve.Classify(candidate, prior(candidate), valid)
	if err != nil {
		t.Fatal(err)
	}
	observed := got.Observed()
	observed[0].LogicalID = "changed"
	if got.Observed()[0].LogicalID == "changed" {
		t.Fatal("result exposed mutable observations")
	}
	if _, err := ownership.Build(bundle, got.Observed()); err != nil {
		t.Fatal(err)
	}
}

func makeCandidate(t *testing.T, version string, ids ...string) (installplan.Plan, artifact.Bundle) {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		families[family] = "families/" + family + ".json"
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": paths(ids, family), "agents": []string{}})
		write(t, root, "routers/"+family+".md", family)
	}
	for _, id := range ids {
		writeJSON(t, root, "manifests/"+id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: id, Family: "reasoning", Source: "sources/" + id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		write(t, root, "sources/"+id+".md", id+version)
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
	projected, err := skillprojection.Build(runtimematrix.RuntimePi, sources)
	if err != nil {
		t.Fatal(err)
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Compatibility: runtimematrix.CompatibilityUnknown}, {ID: runtimematrix.RuntimeClaudeCode, Compatibility: runtimematrix.CompatibilityUnknown}})
	if err != nil {
		t.Fatal(err)
	}
	final, err := projection.BuildPlan(base, []projection.Assessment{projected.Assessment()})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := skillartifact.Build(projected, final)
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := binding.Bundle()
	if !ok {
		t.Fatal("missing bundle")
	}
	symbolic, err := skilldest.Build(binding)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := installplan.BuildWithBundle(resolved, bundle)
	if err != nil {
		t.Fatal(err)
	}
	return plan, bundle
}

func paths(ids []string, family string) []string {
	if family != "reasoning" {
		return []string{}
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "manifests/" + id + ".json"
	}
	return out
}
func writeJSON(t *testing.T, root, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, path, string(data))
}
func write(t *testing.T, root, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, path), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
func hashes(plan installplan.Plan) map[string]string {
	out := map[string]string{}
	for _, file := range plan.Files() {
		if file.Role() == "skill" {
			out[file.LogicalID()] = file.SHA256()
		}
	}
	return out
}
func absent(plan installplan.Plan) []installobserve.SlotObservation {
	slots := make([]installobserve.SlotObservation, 0, len(hashes(plan)))
	for id := range hashes(plan) {
		slots = append(slots, installobserve.SlotObservation{LogicalID: id})
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].LogicalID < slots[j].LogicalID })
	return slots
}
func present(plan installplan.Plan, values map[string]string) []installobserve.SlotObservation {
	slots := absent(plan)
	for i := range slots {
		slots[i].Present, slots[i].SHA256 = true, values[slots[i].LogicalID]
	}
	return slots
}
func prior(plan installplan.Plan) *installobserve.PriorState {
	return &installobserve.PriorState{Manifest: plan.InstalledState(), StateSHA256: hashBytes(plan.StateJSON())}
}
func actions(decisions []ownership.Decision) map[string]ownership.Action {
	out := map[string]ownership.Action{}
	for _, decision := range decisions {
		out[decision.LogicalID] = decision.Action
	}
	return out
}
func hash(value string) string      { return hashBytes([]byte(value)) }
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
