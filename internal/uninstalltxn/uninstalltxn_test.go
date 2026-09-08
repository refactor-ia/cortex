package uninstalltxn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
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

func TestApplyRuntimeHarness(t *testing.T) {
	t.Run("absent state is an idempotent no-op", func(t *testing.T) {
		root := t.TempDir()
		observation := observe(t, root)
		result, err := Apply(root, observation, t.TempDir(), "backup")
		if err != nil || len(result.Actions()) != 0 {
			t.Fatalf("Apply() = (%#v, %v)", result, err)
		}
	})
	t.Run("exact candidates are removed", func(t *testing.T) {
		root, state, files := fixture(t, "alpha")
		result, err := Apply(root, observe(t, root), t.TempDir(), "backup")
		if err != nil || len(result.Actions()) != 2 {
			t.Fatalf("Apply() = (%#v, %v)", result, err)
		}
		assertAbsent(t, files["alpha"])
		assertAbsent(t, state)
	})
	t.Run("missing skill is a no-op", func(t *testing.T) {
		root, state, files := fixture(t, "alpha")
		must(t, os.Remove(files["alpha"]))
		result, err := Apply(root, observe(t, root), t.TempDir(), "backup")
		if err != nil || len(result.Actions()) != 2 || result.Actions()[0].Action != ActionAbsent {
			t.Fatalf("Apply() = (%#v, %v)", result, err)
		}
		assertAbsent(t, state)
	})
	t.Run("global drift blocks every mutation", func(t *testing.T) {
		root, state, files := fixture(t, "alpha", "beta")
		must(t, os.WriteFile(files["alpha"], []byte("user"), 0o600))
		result, err := Apply(root, observe(t, root), t.TempDir(), "backup")
		if !errors.Is(err, ErrConflict) || len(result.Actions()) != 3 {
			t.Fatalf("Apply() = (%#v, %v)", result, err)
		}
		assertData(t, files["alpha"], "user")
		assertData(t, files["beta"], "beta")
		assertData(t, state, string(stateBytes(t, "alpha", "beta")))
	})
}

func TestApplyVerifiedRuntimeHarness(t *testing.T) {
	t.Run("removes exact v2 assets and preserves foreign files", func(t *testing.T) {
		candidate := actorAwareCandidate(t, physicalTempDir(t), "000102030405060708090a0b0c0d0e0f")
		materialize(t, candidate)
		foreign := filepath.Join(candidate.RootPath(), "agents", "user.md")
		must(t, os.WriteFile(foreign, []byte("user"), 0o600))
		result, err := ApplyVerified(candidate, physicalTempDir(t), t.TempDir(), "backup")
		if err != nil || len(result.Actions()) != len(candidate.Files()) {
			t.Fatalf("ApplyVerified() = (%#v, %v)", result, err)
		}
		for _, decision := range result.Actions() {
			if decision.Action != ActionRemove {
				t.Fatalf("decision = %#v, want removal", decision)
			}
		}
		for _, file := range candidate.Files() {
			assertAbsent(t, file.AbsolutePath())
		}
		assertData(t, foreign, "user")
	})

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, candidate installplan.Plan, cwd string) installplan.Plan
		want  error
	}{
		{"installation ID mismatch", func(t *testing.T, candidate installplan.Plan, _ string) installplan.Plan {
			return actorAwareCandidate(t, filepath.Dir(filepath.Dir(candidate.RootPath())), "f00102030405060708090a0b0c0d0e0f")
		}, ErrConflict},
		{"symlink cwd", func(t *testing.T, candidate installplan.Plan, cwd string) installplan.Plan {
			must(t, os.Remove(cwd))
			must(t, os.Symlink(candidate.RootPath(), cwd))
			return candidate
		}, ErrInvalid},
		{"byte drift", func(t *testing.T, candidate installplan.Plan, _ string) installplan.Plan {
			must(t, os.WriteFile(candidate.Files()[0].AbsolutePath(), []byte("drift"), 0o600))
			return candidate
		}, ErrConflict},
		{"mode drift", func(t *testing.T, candidate installplan.Plan, _ string) installplan.Plan {
			must(t, os.Chmod(candidate.Files()[0].AbsolutePath(), 0o644))
			return candidate
		}, ErrConflict},
		{"missing asset", func(t *testing.T, candidate installplan.Plan, _ string) installplan.Plan {
			must(t, os.Remove(candidate.Files()[0].AbsolutePath()))
			return candidate
		}, ErrConflict},
		{"state mismatch", func(t *testing.T, candidate installplan.Plan, _ string) installplan.Plan {
			state := candidate.Files()[len(candidate.Files())-1]
			must(t, os.WriteFile(state.AbsolutePath(), []byte("not-state"), state.DesiredMode()))
			return candidate
		}, ErrFailed},
		{"project shadow", func(t *testing.T, candidate installplan.Plan, cwd string) installplan.Plan {
			shadow := filepath.Join(cwd, ".pi", "agents", "cortex-requirements-analyst.md")
			must(t, os.MkdirAll(filepath.Dir(shadow), 0o700))
			must(t, os.WriteFile(shadow, []byte("foreign"), 0o600))
			return candidate
		}, ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := actorAwareCandidate(t, physicalTempDir(t), "000102030405060708090a0b0c0d0e0f")
			materialize(t, candidate)
			cwd := physicalTempDir(t)
			candidate = tc.setup(t, candidate, cwd)
			before := captureCandidate(t, candidate)
			_, err := ApplyVerified(candidate, cwd, t.TempDir(), "backup")
			if !errors.Is(err, tc.want) {
				t.Fatalf("ApplyVerified() error = %v, want %v", err, tc.want)
			}
			assertUnchangedCandidate(t, candidate, before)
		})
	}
}

func TestApplyVerifiedRollsBackLateStateFailure(t *testing.T) {
	candidate := actorAwareCandidate(t, physicalTempDir(t), "000102030405060708090a0b0c0d0e0f")
	materialize(t, candidate)
	state := candidate.Files()[len(candidate.Files())-1]
	must(t, os.Chmod(filepath.Dir(state.AbsolutePath()), 0o500))
	t.Cleanup(func() { must(t, os.Chmod(filepath.Dir(state.AbsolutePath()), 0o700)) })
	before := captureCandidate(t, candidate)
	_, err := ApplyVerified(candidate, physicalTempDir(t), t.TempDir(), "backup")
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("ApplyVerified() error = %v, want failure", err)
	}
	assertUnchangedCandidate(t, candidate, before)
}

func TestApplyRejectsObservationFromDifferentRoot(t *testing.T) {
	rootA, stateA, filesA := fixture(t, "alpha")
	rootB, stateB, filesB := fixture(t, "alpha")
	_, err := Apply(rootB, observe(t, rootA), t.TempDir(), "backup")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply() error = %v, want ownership conflict", err)
	}
	assertData(t, filesA["alpha"], "alpha")
	assertData(t, stateA, string(stateBytes(t, "alpha")))
	assertData(t, filesB["alpha"], "alpha")
	assertData(t, stateB, string(stateBytes(t, "alpha")))
}

func TestApplyLegacyDoesNotInferActors(t *testing.T) {
	root, _, _ := fixture(t, "alpha")
	actor := filepath.Join(root, "agents", "cortex-requirements-analyst.md")
	must(t, os.MkdirAll(filepath.Dir(actor), 0o700))
	must(t, os.WriteFile(actor, []byte("foreign actor"), 0o600))
	if _, err := Apply(root, observe(t, root), t.TempDir(), "backup"); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	assertData(t, actor, "foreign actor")
}

func TestOperationsPlaceStateLast(t *testing.T) {
	root, _, _ := fixture(t, "alpha", "beta")
	operations, err := operationsFor(observe(t, root))
	if err != nil || len(operations) != 3 || operations[2].Remove == nil || operations[2].Remove.Path != ".cortex/install-state.json" {
		t.Fatalf("operationsFor() = (%#v, %v)", operations, err)
	}
}

func TestApplyRollsBackLateStateFailure(t *testing.T) {
	root, state, files := fixture(t, "alpha")
	observation := observe(t, root)
	stateDir := filepath.Dir(state)
	must(t, os.Chmod(stateDir, 0o500))
	t.Cleanup(func() { must(t, os.Chmod(stateDir, 0o700)) })
	_, err := Apply(root, observation, t.TempDir(), "backup")
	if err == nil {
		t.Fatal("Apply() succeeded despite state removal failure")
	}
	assertData(t, files["alpha"], "alpha")
	assertData(t, state, string(stateBytes(t, "alpha")))
}

func actorAwareCandidate(t *testing.T, home, installationID string) installplan.Plan {
	t.Helper()
	snapshot, err := catalog.BuildCatalogSnapshot(filepath.Join("..", "..", "catalog"), "catalog.json", catalog.AdmissionPolicy{})
	must(t, err)
	sources, err := skillrender.Render(snapshot)
	must(t, err)
	projected, err := skillprojection.Build(runtimematrix.RuntimePi, sources)
	must(t, err)
	assessments, observations := []projection.Assessment{}, []runtimematrix.Observation{}
	for _, runtimeID := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		assessment, err := skillprojection.Build(runtimeID, sources)
		must(t, err)
		assessments = append(assessments, assessment.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: runtimeID, Present: true, Version: "test", Compatibility: runtimematrix.Compatible})
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
	must(t, err)
	plan, err := projection.BuildPlan(base, assessments)
	must(t, err)
	binding, err := skillartifact.Build(projected, plan)
	must(t, err)
	bundle, ok := binding.Bundle()
	if !ok {
		t.Fatal("missing skill bundle")
	}
	destinations, err := skilldest.Build(binding)
	must(t, err)
	resolved, err := skillroot.Resolve(destinations, skillroot.Inputs{Home: home})
	must(t, err)
	skills, err := installplan.BuildWithBundle(resolved, bundle)
	must(t, err)
	actorSources, err := qaactor.Sources(snapshot)
	must(t, err)
	set, err := qaactor.Render(actorSources)
	must(t, err)
	actors, err := qaactor.ProjectPi(set)
	must(t, err)
	actorBinding, err := qaactor.Bind(actors)
	must(t, err)
	candidate, err := installplan.BuildActorAware(skills, actorBinding, installstate.InstallationID(installationID))
	must(t, err)
	return candidate
}

func materialize(t *testing.T, candidate installplan.Plan) {
	t.Helper()
	for _, file := range candidate.Files() {
		must(t, os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700))
		must(t, os.WriteFile(file.AbsolutePath(), file.Content(), file.DesiredMode()))
	}
}

type fileState struct {
	data   []byte
	mode   fs.FileMode
	exists bool
}

func captureCandidate(t *testing.T, candidate installplan.Plan) map[string]fileState {
	t.Helper()
	states := make(map[string]fileState, len(candidate.Files()))
	for _, file := range candidate.Files() {
		data, err := os.ReadFile(file.AbsolutePath())
		if os.IsNotExist(err) {
			states[file.RelativePath()] = fileState{}
			continue
		}
		info, statErr := os.Stat(file.AbsolutePath())
		if err != nil || statErr != nil {
			t.Fatal(err, statErr)
		}
		states[file.RelativePath()] = fileState{data: data, mode: info.Mode().Perm(), exists: true}
	}
	return states
}

func assertUnchangedCandidate(t *testing.T, candidate installplan.Plan, before map[string]fileState) {
	t.Helper()
	for _, file := range candidate.Files() {
		state := before[file.RelativePath()]
		data, err := os.ReadFile(file.AbsolutePath())
		if !state.exists {
			if !os.IsNotExist(err) {
				t.Fatalf("candidate file %q was created", file.RelativePath())
			}
			continue
		}
		info, statErr := os.Stat(file.AbsolutePath())
		if err != nil || statErr != nil || !bytes.Equal(data, state.data) || info.Mode().Perm() != state.mode {
			t.Fatalf("candidate file %q changed", file.RelativePath())
		}
	}
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return path
}

func fixture(t *testing.T, ids ...string) (string, string, map[string]string) {
	t.Helper()
	root, files := t.TempDir(), map[string]string{}
	for _, id := range ids {
		files[id] = filepath.Join(root, "skills", "cortex-"+id, "SKILL.md")
		must(t, os.MkdirAll(filepath.Dir(files[id]), 0o700))
		must(t, os.WriteFile(files[id], []byte(id), 0o600))
	}
	state := filepath.Join(root, ".cortex", "install-state.json")
	must(t, os.MkdirAll(filepath.Dir(state), 0o700))
	must(t, os.WriteFile(state, stateBytes(t, ids...), 0o600))
	return root, state, files
}

func observe(t *testing.T, root string) installobserve.UninstallObservation {
	t.Helper()
	trusted, err := installobserve.NewUninstallRoot(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, root)
	must(t, err)
	observation, err := installobserve.ObserveUninstall(trusted, installobserve.DefaultOptions())
	must(t, err)
	return observation
}

func stateBytes(t *testing.T, ids ...string) []byte {
	t.Helper()
	inputs := make([]installstate.ArtifactInput, 0, len(ids))
	for _, id := range ids {
		digest := sha256.Sum256([]byte(id))
		inputs = append(inputs, installstate.ArtifactInput{LogicalID: "skills/" + id, RelativePath: "skills/cortex-" + id + "/SKILL.md", SHA256: hex.EncodeToString(digest[:])})
	}
	manifest, err := installstate.New(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, hex.EncodeToString(make([]byte, 32)), inputs)
	must(t, err)
	data, err := installstate.Encode(manifest)
	must(t, err)
	return data
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists: %v", path, err)
	}
}
func assertData(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, %v", path, data, err)
	}
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
