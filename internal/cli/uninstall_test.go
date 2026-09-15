package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/skillroot"
	"github.com/refactor-ia/cortex/internal/uninstalltxn"
)

func TestRunUninstallRuntimeHarness(t *testing.T) {
	t.Run("all roots absent are no-op", func(t *testing.T) {
		roots := uninstallRoots(t)
		code, stdout, stderr := runUninstallForTest(t, roots, nil)
		if code != exitOK || stderr != "" || stdout != uninstallLines("not_installed", "not_installed", "not_installed") {
			t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
		}
	})
	t.Run("mixed installed and no-op roots", func(t *testing.T) {
		roots := uninstallRoots(t)
		state, skill := writeInstalled(t, roots[0], "alpha")
		if err := os.MkdirAll(roots[1].RootPath(), 0o700); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runUninstallForTest(t, roots, nil)
		if code != exitOK || stderr != "" || stdout != uninstallLines("completed", "not_installed", "not_installed") {
			t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
		}
		assertMissing(t, skill)
		assertMissing(t, state)
	})
	t.Run("global drift blocks every root before mutation", func(t *testing.T) {
		roots := uninstallRoots(t)
		states, skills := installAll(t, roots)
		if err := os.WriteFile(skills[1], []byte("user-owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runUninstallForTest(t, roots, nil)
		if code != exitConflict || stderr != "" || stdout != uninstallLines("blocked", "conflict", "blocked") {
			t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
		}
		for i := range roots {
			assertPresent(t, states[i])
			assertPresent(t, skills[i])
		}
	})
	t.Run("exact files are removed with state observed last", func(t *testing.T) {
		roots := uninstallRoots(t)
		states, skills := installAll(t, roots)
		code, stdout, stderr := runUninstallForTest(t, roots, func(deps *uninstallDependencies) {
			apply := deps.applyGroup
			calls := 0
			deps.applyGroup = func(requests []uninstalltxn.GroupRequest, backupRoot, backupName string) error {
				calls++
				if len(requests) != 3 {
					t.Fatalf("group requests = %d", len(requests))
				}
				for _, request := range requests {
					records := request.Observation.Records()
					if len(records) != 2 || records[1].LogicalID != "state/install-state" {
						t.Fatalf("state was not observed last: %#v", records)
					}
				}
				return apply(requests, backupRoot, backupName)
			}
			t.Cleanup(func() {
				if calls != 1 {
					t.Errorf("group apply calls = %d", calls)
				}
			})
		})
		if code != exitOK || stderr != "" || stdout != uninstallLines("completed", "completed", "completed") {
			t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
		}
		for i := range roots {
			assertMissing(t, skills[i])
			assertMissing(t, states[i])
		}
	})
	t.Run("group transaction failure reports no completed roots", func(t *testing.T) {
		roots := uninstallRoots(t)
		states, skills := installAll(t, roots)
		code, stdout, stderr := runUninstallForTest(t, roots, func(deps *uninstallDependencies) {
			deps.applyGroup = func([]uninstalltxn.GroupRequest, string, string) error {
				return errors.New("private transaction failure")
			}
		})
		if code != exitTransaction || stderr != "" || stdout != uninstallLines("failed", "failed", "failed") || strings.Contains(stdout, "rollback") {
			t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
		}
		for index := range roots {
			assertPresent(t, skills[index])
			assertPresent(t, states[index])
		}
	})
}

func TestRunUninstallObservationErrorDoesNotMutate(t *testing.T) {
	roots := uninstallRoots(t)
	states, skills := installAll(t, roots)
	code, stdout, stderr := runUninstallForTest(t, roots, func(deps *uninstallDependencies) {
		observe := deps.observe
		calls := 0
		deps.observe = func(root installobserve.UninstallRoot, options installobserve.Options) (installobserve.UninstallObservation, error) {
			calls++
			if calls == 2 {
				return installobserve.UninstallObservation{}, errors.New("private observation failure")
			}
			return observe(root, options)
		}
	})
	if code != exitFailure || stdout != "" || stderr != "error=uninstall_observation_failed\n" {
		t.Fatalf("uninstall = (%d, %q, %q)", code, stdout, stderr)
	}
	for i := range roots {
		assertPresent(t, skills[i])
		assertPresent(t, states[i])
	}
}

func TestRunUninstallRejectsCallerPathsAndFlags(t *testing.T) {
	roots := uninstallRoots(t)
	for _, args := range [][]string{{"uninstall", "--root", roots[0].RootPath()}, {"uninstall", "--force"}, {"uninstall", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := runWithUninstallDependencies(context.Background(), args, &stdout, &stderr, nil, testUninstallDependencies(roots)); code != exitUsage || stdout.Len() != 0 || stderr.String() != "error=invalid_command\n" {
			t.Fatalf("Run(%q) = (%d, %q, %q)", args, code, stdout.String(), stderr.String())
		}
	}
}

func runUninstallForTest(t *testing.T, roots []skillroot.UninstallRoot, change func(*uninstallDependencies)) (int, string, string) {
	t.Helper()
	deps := testUninstallDependencies(roots)
	if change != nil {
		change(&deps)
	}
	var stdout, stderr bytes.Buffer
	code := runWithUninstallDependencies(context.Background(), []string{"uninstall"}, &stdout, &stderr, nil, deps)
	return code, stdout.String(), stderr.String()
}

func testUninstallDependencies(roots []skillroot.UninstallRoot) uninstallDependencies {
	deps := defaultUninstallDependencies()
	deps.resolveRoots = func() ([]skillroot.UninstallRoot, error) {
		return append([]skillroot.UninstallRoot(nil), roots...), nil
	}
	return deps
}

func uninstallRoots(t *testing.T) []skillroot.UninstallRoot {
	t.Helper()
	roots, err := skillroot.ResolveUninstallRoots(skillroot.Inputs{Home: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return roots
}

func installAll(t *testing.T, roots []skillroot.UninstallRoot) ([]string, []string) {
	t.Helper()
	states, skills := make([]string, len(roots)), make([]string, len(roots))
	for i, root := range roots {
		states[i], skills[i] = writeInstalled(t, root, "alpha")
	}
	return states, skills
}

func writeInstalled(t *testing.T, root skillroot.UninstallRoot, content string) (string, string) {
	t.Helper()
	skill := filepath.Join(root.RootPath(), "skills", "cortex-alpha", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := installstate.New(root.RuntimeID(), root.RootKind(), strings.Repeat("0", 64), []installstate.ArtifactInput{{LogicalID: "skills/alpha", RelativePath: "skills/cortex-alpha/SKILL.md", SHA256: hash(content)}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := installstate.Encode(manifest)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root.RootPath(), ".cortex", "install-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	return statePath, skill
}

func uninstallLines(statuses ...string) string {
	var output strings.Builder
	for i, runtime := range []string{"pi", "opencode", "claude-code"} {
		remove, conflict := 2, 0
		if statuses[i] == "not_installed" {
			remove = 0
		} else if statuses[i] == "conflict" {
			remove, conflict = 1, 1
		}
		_, _ = fmt.Fprintf(&output, "runtime=%s uninstall=%s remove=%d absent=0 conflict=%d\n", runtime, statuses[i], remove, conflict)
	}
	return output.String()
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected removed artifact: %v", err)
	}
}

func assertPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected preserved artifact: %v", err)
	}
}
