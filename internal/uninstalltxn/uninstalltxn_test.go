package uninstalltxn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
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

func TestApplyGroupUsesOneStateLastTransaction(t *testing.T) {
	requests, _, _ := groupFixture(t)
	calls := 0
	err := applyGroupWith(requests, filepath.Join(requests[0].Root, ".cortex"), "backup", func(root, backupRoot, backupName string, operations []filetxn.Operation) (filetxn.Snapshot, error) {
		calls++
		if root != filepath.Dir(requests[0].Root) || len(operations) != 6 {
			t.Fatalf("group apply = (%q, %d operations)", root, len(operations))
		}
		for index, operation := range operations {
			state := operation.Remove != nil && filepath.Base(operation.Remove.Path) == "install-state.json"
			if state != (index >= 3) {
				t.Fatalf("operation %d state = %t", index, state)
			}
		}
		return filetxn.Snapshot{}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("ApplyGroup() = (%v, %d calls)", err, calls)
	}
}

func TestApplyGroupRemovesAllRootsOrRestoresAll(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		requests, states, skills := groupFixture(t)
		if err := ApplyGroup(requests, filepath.Join(requests[0].Root, ".cortex"), "backup"); err != nil {
			t.Fatal(err)
		}
		for index := range requests {
			assertAbsent(t, skills[index])
			assertAbsent(t, states[index])
		}
	})
	t.Run("late drift", func(t *testing.T) {
		requests, states, skills := groupFixture(t)
		wantStates := make([]string, len(states))
		for index, state := range states {
			data, err := os.ReadFile(state)
			must(t, err)
			wantStates[index] = string(data)
		}
		must(t, os.WriteFile(skills[2], []byte("user"), 0o600))
		if err := ApplyGroup(requests, filepath.Join(requests[0].Root, ".cortex"), "backup"); !errors.Is(err, ErrFailed) {
			t.Fatalf("ApplyGroup() error = %v", err)
		}
		for index := range requests {
			want := "alpha"
			if index == 2 {
				want = "user"
			}
			assertData(t, skills[index], want)
			assertData(t, states[index], wantStates[index])
		}
	})
}

func TestApplyGroupRejectsUnboundOrOverlappingRoots(t *testing.T) {
	requests, _, _ := groupFixture(t)
	calls := 0
	apply := func(string, string, string, []filetxn.Operation) (filetxn.Snapshot, error) {
		calls++
		return filetxn.Snapshot{}, nil
	}
	unbound := append([]GroupRequest(nil), requests...)
	unbound[1].Observation = requests[0].Observation
	if err := applyGroupWith(unbound, t.TempDir(), "backup", apply); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbound error = %v", err)
	}
	wrongRuntime := []GroupRequest{requests[1]}
	wrongRuntime[0].RuntimeID = runtimematrix.RuntimeClaudeCode
	if err := applyGroupWith(wrongRuntime, t.TempDir(), "backup", apply); !errors.Is(err, ErrConflict) {
		t.Fatalf("runtime mismatch error = %v", err)
	}
	outOfOrder := []GroupRequest{requests[1], requests[0]}
	if err := applyGroupWith(outOfOrder, t.TempDir(), "backup", apply); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out-of-order error = %v", err)
	}
	overlap := append([]GroupRequest(nil), requests[:2]...)
	overlap[1].Root = filepath.Join(overlap[0].Root, "nested")
	must(t, os.MkdirAll(overlap[1].Root, 0o700))
	overlap[1].Observation = observeRuntime(t, runtimematrix.RuntimeOpenCode, skilldest.RootKindOpenCodeUserConfig, overlap[1].Root)
	if err := applyGroupWith(overlap, t.TempDir(), "backup", apply); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overlap error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("apply calls = %d", calls)
	}
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

func groupFixture(t *testing.T) ([]GroupRequest, []string, []string) {
	t.Helper()
	parent := t.TempDir()
	ids := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	kinds := []skilldest.RootKind{skilldest.RootKindPiUserAgent, skilldest.RootKindOpenCodeUserConfig, skilldest.RootKindClaudeCodeUser}
	requests := make([]GroupRequest, len(ids))
	states, skills := make([]string, len(ids)), make([]string, len(ids))
	for index, id := range ids {
		root := filepath.Join(parent, string(id))
		_, state, files := fixtureFor(t, root, id, kinds[index], "alpha")
		states[index], skills[index] = state, files["alpha"]
		requests[index] = GroupRequest{RuntimeID: id, Root: root, Observation: observeRuntime(t, id, kinds[index], root)}
	}
	return requests, states, skills
}

func fixture(t *testing.T, ids ...string) (string, string, map[string]string) {
	t.Helper()
	return fixtureAt(t, t.TempDir(), ids...)
}

func fixtureAt(t *testing.T, root string, ids ...string) (string, string, map[string]string) {
	t.Helper()
	return fixtureFor(t, root, runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, ids...)
}

func fixtureFor(t *testing.T, root string, runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, ids ...string) (string, string, map[string]string) {
	t.Helper()
	files := map[string]string{}
	for _, id := range ids {
		files[id] = filepath.Join(root, "skills", "cortex-"+id, "SKILL.md")
		must(t, os.MkdirAll(filepath.Dir(files[id]), 0o700))
		must(t, os.WriteFile(files[id], []byte(id), 0o600))
	}
	state := filepath.Join(root, ".cortex", "install-state.json")
	must(t, os.MkdirAll(filepath.Dir(state), 0o700))
	must(t, os.WriteFile(state, stateBytesFor(t, runtimeID, rootKind, ids...), 0o600))
	return root, state, files
}

func observe(t *testing.T, root string) installobserve.UninstallObservation {
	t.Helper()
	return observeRuntime(t, runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, root)
}

func observeRuntime(t *testing.T, runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, root string) installobserve.UninstallObservation {
	t.Helper()
	trusted, err := installobserve.NewUninstallRoot(runtimeID, rootKind, root)
	must(t, err)
	observation, err := installobserve.ObserveUninstall(trusted, installobserve.DefaultOptions())
	must(t, err)
	return observation
}

func stateBytes(t *testing.T, ids ...string) []byte {
	t.Helper()
	return stateBytesFor(t, runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, ids...)
}

func stateBytesFor(t *testing.T, runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, ids ...string) []byte {
	t.Helper()
	inputs := make([]installstate.ArtifactInput, 0, len(ids))
	for _, id := range ids {
		digest := sha256.Sum256([]byte(id))
		inputs = append(inputs, installstate.ArtifactInput{LogicalID: "skills/" + id, RelativePath: "skills/cortex-" + id + "/SKILL.md", SHA256: hex.EncodeToString(digest[:])})
	}
	manifest, err := installstate.New(runtimeID, rootKind, hex.EncodeToString(make([]byte, 32)), inputs)
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
