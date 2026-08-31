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
	"github.com/refactor-ia/cortex/internal/installstate"
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

func TestClassifyFilesystemActorAwareCore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, candidate installplan.Plan)
		state ownership.Action
		check func(t *testing.T, candidate installplan.Plan, result installobserve.Result)
	}{
		{
			name:  "fresh creates every typed asset",
			state: ownership.Create,
			check: func(t *testing.T, candidate installplan.Plan, result installobserve.Result) {
				decisions := result.ArtifactDecisions()
				if len(decisions) != len(candidate.Files())-1 || !allAction(decisions, ownership.Create) || !hasKinds(decisions) || !sortedDecisions(decisions) {
					t.Fatalf("ArtifactDecisions() = %#v", decisions)
				}
			},
		},
		{
			name: "fresh present actor conflicts",
			setup: func(t *testing.T, candidate installplan.Plan) {
				writePlanFile(t, actorFileForPlan(t, candidate))
			},
			state: ownership.Create,
			check: func(t *testing.T, _ installplan.Plan, result installobserve.Result) {
				for _, decision := range result.ArtifactDecisions() {
					if decision.Kind == installstate.KindPiActor && decision.Action == ownership.Conflict {
						return
					}
				}
				t.Fatal("present actor did not conflict")
			},
		},
		{
			name:  "v1 migration owns exact skills and creates actors",
			setup: func(t *testing.T, candidate installplan.Plan) { writeV1ObservedSkills(t, candidate, false, false) },
			state: ownership.Replace,
			check: func(t *testing.T, candidate installplan.Plan, result installobserve.Result) {
				assertV1Migration(t, candidate, result, false)
			},
		},
		{
			name:  "v1 migration rejects present actor",
			setup: func(t *testing.T, candidate installplan.Plan) { writeV1ObservedSkills(t, candidate, false, true) },
			state: ownership.Replace,
			check: func(t *testing.T, candidate installplan.Plan, result installobserve.Result) {
				assertV1Migration(t, candidate, result, true)
			},
		},
		{
			name:  "v1 skill drift conflicts",
			setup: func(t *testing.T, candidate installplan.Plan) { writeV1ObservedSkills(t, candidate, true, false) },
			state: ownership.Replace,
			check: func(t *testing.T, _ installplan.Plan, result installobserve.Result) {
				for _, decision := range result.ArtifactDecisions() {
					if decision.Kind == installstate.KindSkill && decision.Action != ownership.Conflict {
						t.Fatalf("skill decision = %#v, want conflict", decision)
					}
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := makeActorAwareCandidate(t)
			if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, candidate)
			}
			observation, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			result, err := installobserve.ClassifyFilesystem(candidate, observation)
			if err != nil || result.StateAction() != tc.state {
				t.Fatalf("ClassifyFilesystem() = (%#v, %v)", result, err)
			}
			tc.check(t, candidate, result)
		})
	}
}

func TestClassifyFilesystemRejectsPriorV2AndDetachesDecisions(t *testing.T) {
	candidate := makeActorAwareCandidate(t)
	writeCandidateFiles(t, candidate)
	observation, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	result, err := installobserve.ClassifyFilesystem(candidate, observation)
	if err == nil || result.StateAction() != "" || len(result.ArtifactDecisions()) != 0 || len(result.Observed()) != 0 {
		t.Fatalf("ClassifyFilesystem() = (%#v, %v), want empty rejected result", result, err)
	}

	candidate = makeActorAwareCandidate(t)
	if err := os.MkdirAll(candidate.RootPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	observation, err = installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	result, err = installobserve.ClassifyFilesystem(candidate, observation)
	if err != nil {
		t.Fatal(err)
	}
	decisions := result.ArtifactDecisions()
	decisions[0].LogicalID = "changed"
	if result.ArtifactDecisions()[0].LogicalID == "changed" {
		t.Fatal("result exposed mutable artifact decisions")
	}
}

func writeV1ObservedSkills(t *testing.T, candidate installplan.Plan, drift, actor bool) {
	t.Helper()
	inputs := make([]installstate.ArtifactInput, 0)
	for _, artifact := range candidate.InstalledState().Artifacts() {
		if artifact.Kind() == installstate.KindSkill {
			inputs = append(inputs, installstate.ArtifactInput{LogicalID: artifact.LogicalID(), RelativePath: artifact.RelativePath(), SHA256: artifact.SHA256()})
		}
	}
	prior, err := installstate.New(candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	state, err := installstate.Encode(prior)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(candidate.RootPath(), ".cortex"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidate.RootPath(), ".cortex", "install-state.json"), state, installplan.CanonicalFileMode); err != nil {
		t.Fatal(err)
	}
	for _, file := range candidate.Files() {
		if file.Role() == "skill" {
			content := file.Content()
			if drift {
				content = []byte("drift")
			}
			if err := os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file.AbsolutePath(), content, installplan.CanonicalFileMode); err != nil {
				t.Fatal(err)
			}
		}
		if actor && file.Role() == "actor" {
			writePlanFile(t, file)
		}
	}
}

func writePlanFile(t *testing.T, file installplan.File) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.AbsolutePath(), file.Content(), 0o640); err != nil {
		t.Fatal(err)
	}
}

func actorFileForPlan(t *testing.T, candidate installplan.Plan) installplan.File {
	t.Helper()
	for _, file := range candidate.Files() {
		if file.Role() == "actor" {
			return file
		}
	}
	t.Fatal("missing actor")
	return installplan.File{}
}

func assertV1Migration(t *testing.T, candidate installplan.Plan, result installobserve.Result, actorConflict bool) {
	t.Helper()
	for _, decision := range result.ArtifactDecisions() {
		switch decision.Kind {
		case installstate.KindSkill:
			if decision.Action != ownership.Unchanged || decision.ObservedOwnership != ownership.CortexOwned {
				t.Fatalf("skill decision = %#v", decision)
			}
		case installstate.KindPiActor:
			want := ownership.Create
			if actorConflict {
				want = ownership.Conflict
			}
			if decision.Action != want {
				t.Fatalf("actor decision = %#v, want %q", decision, want)
			}
		}
	}
	bundle, found := candidate.Bundle()
	if !found {
		t.Fatal("candidate lacks bundle")
	}
	plan, err := ownership.Build(bundle, result.Observed())
	if err != nil || !plan.Ready() {
		t.Fatalf("ownership.Build() = (%#v, %v)", plan, err)
	}
	for _, observed := range result.Observed() {
		if observed.LogicalID[:7] != "skills/" {
			t.Fatalf("Observed() included non-skill %#v", observed)
		}
	}
}

func allAction(decisions []installobserve.ArtifactDecision, action ownership.Action) bool {
	for _, decision := range decisions {
		if decision.Action != action {
			return false
		}
	}
	return true
}

func hasKinds(decisions []installobserve.ArtifactDecision) bool {
	kinds := map[installstate.Kind]bool{}
	for _, decision := range decisions {
		kinds[decision.Kind] = true
	}
	return kinds[installstate.KindSkill] && kinds[installstate.KindPiActor]
}

func sortedDecisions(decisions []installobserve.ArtifactDecision) bool {
	return sort.SliceIsSorted(decisions, func(i, j int) bool { return decisions[i].LogicalID < decisions[j].LogicalID })
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
