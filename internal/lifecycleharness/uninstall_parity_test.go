package lifecycleharness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillroot"
	"github.com/refactor-ia/cortex/internal/uninstalltxn"
)

// The compatible observations used by this harness are synthetic fixture inputs only.
// These tests provide structural coverage, not real runtime compatibility certification.
func TestSyntheticCompatibleUninstallParity(t *testing.T) {
	home := t.TempDir()
	plans := buildPlans(t, home, "uninstall", "alpha", "beta")
	assertDistinctUninstallShapes(t, plans, home)

	for _, tc := range runtimeCases(home) {
		t.Run(string(tc.runtime), func(t *testing.T) {
			t.Run("exact then absent is idempotent", func(t *testing.T) {
				plan, root := uninstallFixture(t, tc.runtime)
				config, before := seedUserConfig(t, plan)
				result, err := uninstalltxn.Apply(plan.RootPath(), observeUninstall(t, root), t.TempDir(), "uninstall")
				if err != nil {
					t.Fatal(err)
				}
				assertUninstallActions(t, result, plan, uninstalltxn.ActionRemove)
				assertRemovedCortexFiles(t, plan)
				assertPreservedUserRoot(t, plan, config, before)

				result, err = uninstalltxn.Apply(plan.RootPath(), observeUninstall(t, root), t.TempDir(), "uninstall-repeat")
				if err != nil || len(result.Actions()) != 0 {
					t.Fatalf("repeat uninstall = (%#v, %v)", result, err)
				}
				assertPreservedUserRoot(t, plan, config, before)
			})

			t.Run("missing skill preserves unrelated config", func(t *testing.T) {
				plan, root := uninstallFixture(t, tc.runtime)
				config, before := seedUserConfig(t, plan)
				if err := os.Remove(plan.Files()[0].AbsolutePath()); err != nil {
					t.Fatal(err)
				}
				result, err := uninstalltxn.Apply(plan.RootPath(), observeUninstall(t, root), t.TempDir(), "uninstall-missing")
				if err != nil {
					t.Fatal(err)
				}
				actions := result.Actions()
				if len(actions) != len(plan.Files()) || actions[0].Action != uninstalltxn.ActionAbsent {
					t.Fatalf("missing skill actions = %#v", actions)
				}
				for _, action := range actions[1:] {
					if action.Action != uninstalltxn.ActionRemove {
						t.Fatalf("missing skill suppressed removal: %#v", actions)
					}
				}
				assertRemovedCortexFiles(t, plan)
				assertPreservedUserRoot(t, plan, config, before)
			})

			t.Run("drift globally suppresses removal", func(t *testing.T) {
				plan, root := uninstallFixture(t, tc.runtime)
				config, before := seedUserConfig(t, plan)
				drifted := len(plan.Files()) - 2 // The final skill proves later drift suppresses earlier removal.
				if err := os.WriteFile(plan.Files()[drifted].AbsolutePath(), []byte("user drift"), installplan.CanonicalFileMode); err != nil {
					t.Fatal(err)
				}
				files := readFiles(t, plan.Files())
				result, err := uninstalltxn.Apply(plan.RootPath(), observeUninstall(t, root), t.TempDir(), "uninstall-drift")
				if !errors.Is(err, uninstalltxn.ErrConflict) {
					t.Fatalf("drift uninstall error = %v", err)
				}
				actions := result.Actions()
				if len(actions) != len(plan.Files()) || actions[drifted].Action != uninstalltxn.ActionConflict {
					t.Fatalf("drift actions = %#v", actions)
				}
				assertFilesUnchanged(t, files, plan.Files())
				assertPreservedUserRoot(t, plan, config, before)
			})

			t.Run("cross root evidence is rejected", func(t *testing.T) {
				source, sourceRoot := uninstallFixture(t, tc.runtime)
				target, _ := uninstallFixture(t, tc.runtime)
				config, before := seedUserConfig(t, target)
				files := readFiles(t, target.Files())
				result, err := uninstalltxn.Apply(target.RootPath(), observeUninstall(t, sourceRoot), t.TempDir(), "uninstall-cross-root")
				if !errors.Is(err, uninstalltxn.ErrConflict) || len(result.Actions()) != 0 {
					t.Fatalf("cross-root uninstall = (%#v, %v)", result, err)
				}
				assertFilesUnchanged(t, files, target.Files())
				assertPreservedUserRoot(t, target, config, before)
				assertMaterialized(t, source)
			})

			t.Run("late state failure rolls back skill removal", func(t *testing.T) {
				plan, root := uninstallFixture(t, tc.runtime)
				config, before := seedUserConfig(t, plan)
				state := plan.Files()[len(plan.Files())-1].AbsolutePath()
				if err := os.Chmod(filepath.Dir(state), 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(filepath.Dir(state), 0o700); err != nil {
						t.Error(err)
					}
				})
				if _, err := uninstalltxn.Apply(plan.RootPath(), observeUninstall(t, root), t.TempDir(), "uninstall-rollback"); err == nil {
					t.Fatal("late state removal failure unexpectedly succeeded")
				}
				assertMaterialized(t, plan)
				assertPreservedUserRoot(t, plan, config, before)
			})
		})
	}
}

func uninstallFixture(t *testing.T, runtime runtimematrix.RuntimeID) (installplan.Plan, installobserve.UninstallRoot) {
	t.Helper()
	home := t.TempDir()
	plan := buildPlans(t, home, "uninstall", "alpha", "beta")[runtime]
	assertRuntimeShape(t, plan, runtimeCaseFor(t, home, runtime))
	mustMkdir(t, plan.RootPath())
	assertActions(t, apply(t, plan), plan, "create")
	return plan, canonicalUninstallRoot(t, home, plan)
}

func assertDistinctUninstallShapes(t *testing.T, plans map[runtimematrix.RuntimeID]installplan.Plan, home string) {
	t.Helper()
	roots, kinds := map[string]bool{}, map[string]bool{}
	for _, tc := range runtimeCases(home) {
		plan := plans[tc.runtime]
		assertRuntimeShape(t, plan, tc)
		if roots[plan.RootPath()] || kinds[string(plan.RootKind())] {
			t.Fatal("runtime canonical roots or kinds were not distinct")
		}
		roots[plan.RootPath()], kinds[string(plan.RootKind())] = true, true
	}
}

func runtimeCaseFor(t *testing.T, home string, runtime runtimematrix.RuntimeID) runtimeCase {
	t.Helper()
	for _, candidate := range runtimeCases(home) {
		if candidate.runtime == runtime {
			return candidate
		}
	}
	t.Fatal("missing runtime harness case")
	return runtimeCase{}
}

func canonicalUninstallRoot(t *testing.T, home string, plan installplan.Plan) installobserve.UninstallRoot {
	t.Helper()
	roots, err := skillroot.ResolveUninstallRoots(skillroot.Inputs{Home: home})
	must(t, err, "resolve canonical uninstall roots")
	for _, root := range roots {
		if root.RuntimeID() == plan.RuntimeID() {
			if root.RootKind() != plan.RootKind() || root.RootPath() != plan.RootPath() {
				t.Fatal("uninstall root did not match the installed runtime identity")
			}
			trusted, err := installobserve.NewUninstallRoot(root.RuntimeID(), root.RootKind(), root.RootPath())
			must(t, err, "construct trusted uninstall root")
			return trusted
		}
	}
	t.Fatal("canonical uninstall root was not resolved")
	return installobserve.UninstallRoot{}
}

func observeUninstall(t *testing.T, root installobserve.UninstallRoot) installobserve.UninstallObservation {
	t.Helper()
	observation, err := installobserve.ObserveUninstall(root, installobserve.DefaultOptions())
	must(t, err, "observe uninstall root")
	return observation
}

func seedUserConfig(t *testing.T, plan installplan.Plan) (string, []byte) {
	t.Helper()
	path, data := filepath.Join(plan.RootPath(), "user-settings.json"), []byte(`{"user":"preserved"}`)
	must(t, os.WriteFile(path, data, 0o600), "seed unrelated user config")
	return path, data
}

func assertUninstallActions(t *testing.T, result uninstalltxn.Result, plan installplan.Plan, want uninstalltxn.Action) {
	t.Helper()
	actions := result.Actions()
	if len(actions) != len(plan.Files()) {
		t.Fatalf("uninstall actions = %#v", actions)
	}
	for index, action := range actions {
		if action.LogicalID != plan.Files()[index].LogicalID() || action.Action != want {
			t.Fatalf("uninstall action %d = %#v", index, action)
		}
	}
	if actions[len(actions)-1].LogicalID != "state/install-state" {
		t.Fatal("uninstall state was not final")
	}
}

func assertRemovedCortexFiles(t *testing.T, plan installplan.Plan) {
	t.Helper()
	for _, file := range plan.Files() {
		if _, err := os.Lstat(file.AbsolutePath()); !os.IsNotExist(err) {
			t.Fatalf("Cortex file remains at %s: %v", file.AbsolutePath(), err)
		}
	}
}

func assertPreservedUserRoot(t *testing.T, plan installplan.Plan, config string, want []byte) {
	t.Helper()
	data, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("unrelated user config changed: %q, %v", data, err)
	}
	for _, path := range []string{plan.RootPath(), filepath.Dir(plan.RootPath())} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("shared runtime root parent was removed: %s: %v", path, err)
		}
	}
}
