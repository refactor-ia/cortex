package installplan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

const actorAwareInstallationID installstate.InstallationID = "000102030405060708090a0b0c0d0e0f"

func TestBuildActorAwareCreatesBoundV2Candidate(t *testing.T) {
	skills, actors := actorAwareInputs(t, runtimematrix.RuntimePi)

	plan, err := BuildActorAware(skills, actors, actorAwareInstallationID)
	if err != nil {
		t.Fatal(err)
	}
	bundle, found := plan.Bundle()
	if !found || bundle.Manifest().SnapshotFingerprint() != plan.SnapshotFingerprint() ||
		plan.InstalledState().SchemaVersion() != 2 || plan.InstalledState().InstallationID() != actorAwareInstallationID ||
		!bytes.Equal(plan.StateJSON(), mustEncode(t, plan.InstalledState())) {
		t.Fatalf("BuildActorAware() returned an incomplete v2 candidate")
	}
	files, skillFiles := plan.Files(), skills.Files()
	if len(files) != len(skillFiles)-1+7 {
		t.Fatalf("file count = %d, want %d", len(files), len(skillFiles)-1+7)
	}
	destinations := qaactorMustDestinations(t, actors)
	destinationsByID := make(map[string]qaactor.Destination, len(destinations))
	for index, destination := range destinations {
		destinationsByID[destination.LogicalID()] = destination
		file := files[len(skillFiles)-1+index]
		if file.Role() != "actor" || file.LogicalID() != destination.LogicalID() {
			t.Fatalf("actor output %d = (%q, %q), want (%q, %q)", index, file.Role(), file.LogicalID(), "actor", destination.LogicalID())
		}
	}
	for index, skill := range skillFiles[:len(skillFiles)-1] {
		file := files[index]
		if file.Role() != "skill" || file.LogicalID() != skill.LogicalID() {
			t.Fatalf("skill output %d = (%q, %q), want (%q, %q)", index, file.Role(), file.LogicalID(), "skill", skill.LogicalID())
		}
	}
	filesByID := make(map[string]File, len(files)-1)
	for _, file := range files[:len(files)-1] {
		if file.DesiredMode() != CanonicalFileMode || digest(file.Content()) != file.SHA256() {
			t.Fatalf("non-state file %q has a noncanonical mode or digest", file.LogicalID())
		}
		filesByID[file.LogicalID()] = file
	}
	if artifacts := plan.InstalledState().Artifacts(); len(artifacts) != len(filesByID) {
		t.Fatalf("v2 artifacts = %d, want %d", len(artifacts), len(filesByID))
	} else {
		for _, artifact := range artifacts {
			file, found := filesByID[artifact.LogicalID()]
			if !found || artifact.RelativePath() != file.RelativePath() || artifact.SHA256() != file.SHA256() || artifact.InstallationID() != actorAwareInstallationID {
				t.Fatalf("v2 artifact %q does not match an output file", artifact.LogicalID())
			}
			switch artifact.Kind() {
			case installstate.KindSkill:
				if file.Role() != "skill" || artifact.CapabilityID() != artifact.LogicalID()[len("skills/"):] || artifact.RoleID() != "" || artifact.ActorContractVersion() != "" {
					t.Fatalf("skill artifact %q has invalid typed ownership", artifact.LogicalID())
				}
			case installstate.KindPiActor:
				actor, found := destinationsByID[artifact.LogicalID()]
				if !found || file.Role() != "actor" || string(artifact.Kind()) != actor.Kind() || artifact.RoleID() != actor.RoleID() || artifact.ActorContractVersion() != actor.ActorContract() || artifact.CapabilityID() != "" || artifact.RelativePath() != actor.RelativePath() || !bytes.Equal(file.Content(), actor.Content()) {
					t.Fatalf("actor artifact %q does not match its canonical destination", artifact.LogicalID())
				}
			default:
				t.Fatalf("unexpected v2 artifact kind %q", artifact.Kind())
			}
		}
	}
	state := files[len(files)-1]
	if state.Role() != "state" || state.RelativePath() != stateRelativePath || state.AbsolutePath() != filepath.Join(plan.RootPath(), filepath.FromSlash(stateRelativePath)) ||
		state.DesiredMode() != CanonicalFileMode || state.SHA256() != digest(state.Content()) || !bytes.Equal(state.Content(), plan.StateJSON()) {
		t.Fatal("state is not the final canonical candidate")
	}
}

func TestBuildActorAwareRejectsUnboundOrTamperedInputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime runtimematrix.RuntimeID
		mutate  func(*Plan, *qaactor.Binding, *installstate.InstallationID)
	}{
		{"unbound v1 skills", runtimematrix.RuntimePi, func(plan *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) { plan.hasBundle = false }},
		{"non Pi skills", runtimematrix.RuntimeOpenCode, func(_ *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) {}},
		{"zero actor binding", runtimematrix.RuntimePi, func(_ *Plan, binding *qaactor.Binding, _ *installstate.InstallationID) { *binding = qaactor.Binding{} }},
		{"invalid installation ID", runtimematrix.RuntimePi, func(_ *Plan, _ *qaactor.Binding, id *installstate.InstallationID) { *id = "invalid" }},
		{"tampered skill bytes", runtimematrix.RuntimePi, func(plan *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) {
			plan.files[0].content = append(plan.files[0].content, 'X')
		}},
		{"tampered state bytes", runtimematrix.RuntimePi, func(plan *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) {
			plan.files[len(plan.files)-1].content = []byte("{}")
		}},
		{"tampered retained manifest", runtimematrix.RuntimePi, func(plan *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) {
			plan.installedState = installstate.Manifest{}
		}},
		{"tampered fingerprint", runtimematrix.RuntimePi, func(plan *Plan, _ *qaactor.Binding, _ *installstate.InstallationID) {
			plan.snapshotFingerprint = strings.Repeat("a", 64)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skills, actors := actorAwareInputs(t, tc.runtime)
			id := actorAwareInstallationID
			tc.mutate(&skills, &actors, &id)
			if plan, err := BuildActorAware(skills, actors, id); err == nil || len(plan.Files()) != 0 {
				t.Fatalf("BuildActorAware() = (%#v, %v), want rejection", plan, err)
			}
		})
	}
}

func TestBuildActorAwareReturnsDetachedCandidate(t *testing.T) {
	skills, actors := actorAwareInputs(t, runtimematrix.RuntimePi)
	plan, err := BuildActorAware(skills, actors, actorAwareInstallationID)
	if err != nil {
		t.Fatal(err)
	}
	skills.files[0].content[0] ^= 1
	files := plan.Files()
	files[0].content[0] ^= 1
	files[len(files)-1].content[0] ^= 1
	fresh := plan.Files()
	if fresh[0].Content()[0] == skills.files[0].content[0] || fresh[0].Content()[0] == files[0].content[0] ||
		fresh[len(fresh)-1].Content()[0] == files[len(files)-1].content[0] {
		t.Fatal("BuildActorAware() exposed shared candidate bytes")
	}
}

func TestBuildActorAwareRejectsMismatchedActorFingerprint(t *testing.T) {
	skills, _ := actorAwareInputs(t, runtimematrix.RuntimePi)
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(filepath.Join("..", "..", "catalog"))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "families", "quality-assurance", "sources", "test-runner.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, actors := actorAwareInputsAt(t, root, runtimematrix.RuntimePi)
	if plan, err := BuildActorAware(skills, actors, actorAwareInstallationID); err == nil || len(plan.Files()) != 0 {
		t.Fatalf("BuildActorAware() = (%#v, %v), want mismatched fingerprint rejection", plan, err)
	}
}

func actorAwareInputs(t *testing.T, runtime runtimematrix.RuntimeID) (Plan, qaactor.Binding) {
	t.Helper()
	return actorAwareInputsAt(t, filepath.Join("..", "..", "catalog"), runtime)
}

func actorAwareInputsAt(t *testing.T, root string, runtime runtimematrix.RuntimeID) (Plan, qaactor.Binding) {
	t.Helper()
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
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, id := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		candidate, err := skillprojection.Build(id, sources)
		if err != nil {
			t.Fatal(err)
		}
		assessments = append(assessments, candidate.Assessment())
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
	binding, err := skillartifact.Build(projected, final)
	if err != nil {
		t.Fatal(err)
	}
	bundle, ok := binding.Bundle()
	if !ok {
		t.Fatal("missing skill bundle")
	}
	symbolic, err := skilldest.Build(binding)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	skills, err := BuildWithBundle(resolved, bundle)
	if err != nil {
		t.Fatal(err)
	}
	actorSources, err := qaactor.Sources(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	set, err := qaactor.Render(actorSources)
	if err != nil {
		t.Fatal(err)
	}
	actors, err := qaactor.ProjectPi(set)
	if err != nil {
		t.Fatal(err)
	}
	result, err := qaactor.Bind(actors)
	if err != nil {
		t.Fatal(err)
	}
	return skills, result
}

func qaactorMustDestinations(t *testing.T, binding qaactor.Binding) []qaactor.Destination {
	t.Helper()
	plan, err := qaactor.PlanDestinations(binding)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Destinations()
}
