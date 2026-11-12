package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// directoryLifecycleInstall installs the shipped catalog into a target
// directory through the production dispatch, so lifecycle tests start from a
// real installed state.
func directoryLifecycleInstall(t *testing.T, home, target string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"install", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK {
		t.Fatalf("install exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestUpdateDirectoryTargetPlansAndApplies(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLifecycleInstall(t, home, target)
	fixture := newUpdateFixture(t)

	skill := func() string {
		data, err := os.ReadFile(filepath.Join(target, "skills", "cortex-requirements-analyst", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	before := skill()

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", fixture.patched}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("plan exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime=directory") || !strings.Contains(stdout.String(), "mode=plan") || !strings.Contains(stdout.String(), "status=planned") {
		t.Fatalf("plan output = %q", stdout.String())
	}
	if skill() != before {
		t.Fatal("a read-only plan must not write")
	}

	stdout.Reset()
	stderr.Reset()
	code = runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", fixture.patched, "--apply"}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("apply exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "status=applied") || !strings.Contains(stdout.String(), "runtime=directory") {
		t.Fatalf("apply output = %q", stdout.String())
	}
	if skill() == before {
		t.Fatal("apply must refresh the projected skill")
	}
}

func TestUpdateDirectoryTargetFailsClosedOnUnknownState(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "never-installed")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"update", "--target-dir", target, "--catalog", "../../catalog"}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code == exitOK || !strings.Contains(stdout.String(), "status=not_applied") {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestUninstallDirectoryTargetRemovesOnlyOwnedFiles(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	directoryLifecycleInstall(t, home, target)
	sentinel := filepath.Join(target, "foreign-sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("user bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"uninstall", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime=directory uninstall=completed") {
		t.Fatalf("uninstall output = %q", stdout.String())
	}
	remaining, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	// Only the foreign file, the shared skills directory, and the .cortex
	// directory holding the uninstall backup may remain: every Cortex-owned
	// skill and the state file are gone.
	names := map[string]bool{}
	for _, entry := range remaining {
		names[entry.Name()] = true
	}
	if !names["foreign-sentinel.txt"] || !names["skills"] || !names[".cortex"] || len(names) != 3 {
		t.Fatalf("uninstall must keep foreign files and remove owned ones, left: %v", remaining)
	}
	if _, err := os.Stat(filepath.Join(target, ".cortex", "install-state.json")); !os.IsNotExist(err) {
		t.Fatalf("state file must be removed: %v", err)
	}
	// The uninstall transaction removes files, not directories (no directory
	// removal authority): empty cortex-* directories may remain, but no owned
	// file may survive anywhere under skills/.
	leftover, err := filepath.Glob(filepath.Join(target, "skills", "cortex-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftover) != 0 {
		t.Fatalf("owned skill files must be removed, left: %v", leftover)
	}
}

// A missing target directory trivially has no prior Cortex ownership, so
// uninstall fails closed exactly like an existing directory without
// ownership records (issue #358) and creates nothing on disk.
func TestUninstallDirectoryTargetFailsClosedWhenRootMissing(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "typo-dir")

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"uninstall", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitConflict {
		t.Fatalf("exit = %d, want %d", code, exitConflict)
	}
	if got, want := stdout.String(), "runtime=directory uninstall=not_installed remove=0 absent=0 conflict=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("missing target must not be created: %v", err)
	}
}

// An existing directory without Cortex ownership keeps failing closed.
func TestUninstallDirectoryTargetFailsClosedWhenNotInstalled(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), []string{"uninstall", "--target-dir", target}, &stdout, &stderr, nil, directoryInstallDependencies(t, home), defaultUninstallDependencies())
	if code != exitConflict {
		t.Fatalf("exit = %d, want %d", code, exitConflict)
	}
	if got, want := stdout.String(), "runtime=directory uninstall=not_installed remove=0 absent=0 conflict=0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("target must stay empty: %v %v", entries, err)
	}
}
