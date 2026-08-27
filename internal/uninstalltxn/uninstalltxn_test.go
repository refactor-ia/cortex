package uninstalltxn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
