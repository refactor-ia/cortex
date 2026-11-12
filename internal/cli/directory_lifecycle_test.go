package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/skillroot"
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

// TestUpdateDirectoryTargetRefusesMissingOrNonDirectory pins the #390 ruling:
// update refuses an absolute target that is missing or not a directory as
// invalid_target, the same as install, and writes nothing.
func TestUpdateDirectoryTargetRefusesMissingOrNonDirectory(t *testing.T) {
	for _, apply := range []bool{false, true} {
		for _, kind := range []string{"missing", "file"} {
			name := kind
			if apply {
				name += "/apply"
			}
			t.Run(name, func(t *testing.T) {
				home := canonicalHome(t)
				target := filepath.Join(home, "agent-skills")
				if kind == "file" {
					if err := os.WriteFile(target, []byte("user bytes"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				before := homeListing(t, home)
				args := []string{"update", "--target-dir", target, "--catalog", "../../catalog"}
				if apply {
					args = append(args, "--apply")
				}
				code, stdout, stderr := runRefusal(t, home, args)
				if code != exitUsage || stdout != "" || stderr != invalidTargetRefusal {
					t.Fatalf("exit = %d stdout = %q stderr = %q, want %d \"\" %q", code, stdout, stderr, exitUsage, invalidTargetRefusal)
				}
				if after := homeListing(t, home); after != before {
					t.Fatalf("home changed on refusal: %q, want %q", after, before)
				}
			})
		}
	}
}

// Doctor keeps reporting a missing absolute target instead of refusing it
// (#390): exit 0, presence=absent, and nothing created.
func TestDoctorDirectoryTargetReportsMissingAsAbsent(t *testing.T) {
	home := canonicalHome(t)
	target := filepath.Join(home, "agent-skills")
	code, stdout, stderr := runRefusal(t, home, []string{"doctor", "--target-dir", target})
	if code != exitOK || stderr != "" || stdout != "target="+target+" presence=absent\n" {
		t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("missing target must not be created: %v", err)
	}
}

// homeListing lists every path and file content under root.
func homeListing(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		line := path
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			line += "=" + string(data)
		}
		entries = append(entries, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(entries, "\n")
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

// guardedUninstallDependencies fails the test if a command falls back to the
// canonical all-roots uninstall, so a refusal test can never reach the real
// user home even when the refusal is missing.
func guardedUninstallDependencies(t *testing.T) uninstallDependencies {
	t.Helper()
	deps := defaultUninstallDependencies()
	deps.resolveRoots = func() ([]skillroot.UninstallRoot, error) {
		t.Error("canonical uninstall roots were resolved")
		return nil, errors.New("canonical uninstall roots must not be resolved")
	}
	return deps
}

// TestDirectoryTargetCommandsRefuseRelativeTargets pins issue #372: every
// command that consumes --target-dir refuses a path install would refuse,
// with the same diagnostic and exit code, and writes nothing.
func TestDirectoryTargetCommandsRefuseRelativeTargets(t *testing.T) {
	const existing = "test-relative-target-372"
	shipped, err := filepath.Abs(filepath.Join("..", "..", "catalog"))
	if err != nil {
		t.Fatal(err)
	}
	// The relative fixture lives in a temporary working directory, never in
	// the package source tree.
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "foreign-sentinel.txt"), []byte("user bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	listing := func(root string) string {
		var names []string
		err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
			names = append(names, path)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(names, "\n")
	}
	before := listing(existing)
	// Extra arguments make each command reach its target check: update gets a
	// valid catalog and --apply so a refusal cannot come from the catalog.
	commands := map[string][]string{
		"install":   nil,
		"doctor":    nil,
		"update":    {"--catalog", shipped, "--apply"},
		"uninstall": nil,
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	// unclean is absolute and names the existing directory, so only the clean
	// check can refuse it. trailing-separator is the form shell completion
	// produces. empty is what an unset shell variable passes.
	unclean := cwd + string(filepath.Separator) + "." + string(filepath.Separator) + existing
	trailing := filepath.Join(cwd, existing) + string(filepath.Separator)
	targets := map[string]string{"existing": existing, "missing": "relative/missing-target", "unclean": unclean, "trailing-separator": trailing, "empty": ""}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(cwd)
		targets["drive-relative"] = volume + existing
		targets["rooted-without-drive"] = strings.TrimPrefix(cwd, volume) + `\` + existing
		targets["mixed-separators"] = cwd + "/" + existing
	}
	for command, extra := range commands {
		for name, target := range targets {
			t.Run(command+"/"+name, func(t *testing.T) {
				home := canonicalHome(t)
				args := append([]string{command, "--target-dir", target}, extra...)
				code, stdout, stderr := runRefusal(t, home, args)
				if code != exitUsage || stdout != "" || stderr != invalidTargetRefusal {
					t.Fatalf("exit = %d stdout = %q stderr = %q, want %d \"\" %q", code, stdout, stderr, exitUsage, invalidTargetRefusal)
				}
				if after := listing(existing); after != before {
					t.Fatalf("relative target changed: %q, want %q", after, before)
				}
				if after := listing(home); after != home {
					t.Fatalf("home changed on refusal: %q", after)
				}
				if _, err := os.Stat("relative"); !os.IsNotExist(err) {
					t.Fatalf("missing relative target was created: %v", err)
				}
			})
		}
	}
	t.Run("install/equals-form", func(t *testing.T) {
		code, stdout, stderr := runRefusal(t, canonicalHome(t), []string{"install", "--target-dir=" + existing})
		if code != exitUsage || stdout != "" || stderr != invalidTargetRefusal {
			t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
		}
	})
	t.Run("update/target-checked-before-catalog", func(t *testing.T) {
		code, stdout, stderr := runRefusal(t, canonicalHome(t), []string{"update", "--target-dir", existing, "--catalog", "missing-catalog", "--apply"})
		if code != exitUsage || stdout != "" || stderr != invalidTargetRefusal {
			t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
		}
	})
	// An explicit empty --runtime is a usage error, so the runtime mode never
	// reaches the directory mode through an empty runtime ID.
	t.Run("update/empty-runtime-is-a-usage-error", func(t *testing.T) {
		code, stdout, stderr := runRefusal(t, canonicalHome(t), []string{"update", "--runtime", "", "--catalog", shipped})
		if code != exitUsage || stdout != "" || stderr != "error=invalid_command\n" {
			t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
		}
	})
	// Contradictory modes are a usage error even when the runtime is empty,
	// the same as with a non-empty runtime.
	t.Run("update/empty-runtime-with-target-is-a-usage-error", func(t *testing.T) {
		code, stdout, stderr := runRefusal(t, canonicalHome(t), []string{"update", "--runtime", "", "--target-dir", filepath.Join(cwd, existing), "--catalog", shipped})
		if code != exitUsage || stdout != "" || stderr != "error=invalid_command\n" {
			t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
		}
	})
	// An empty first --target-dir still counts as given, so a second one is a
	// duplicate flag rather than a silent override.
	for command, extra := range map[string][]string{"doctor": nil, "update": {"--catalog", shipped}, "uninstall": nil} {
		t.Run(command+"/duplicate-target-after-empty", func(t *testing.T) {
			args := append([]string{command, "--target-dir", "", "--target-dir", filepath.Join(cwd, existing)}, extra...)
			code, stdout, stderr := runRefusal(t, canonicalHome(t), args)
			if code != exitUsage || stdout != "" || stderr != "error=invalid_command\n" {
				t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
			}
			if after := listing(existing); after != before {
				t.Fatalf("target changed: %q, want %q", after, before)
			}
		})
	}
	// Control: the shared check must not over-refuse a valid absolute target.
	t.Run("doctor/accepts-valid-absolute", func(t *testing.T) {
		code, stdout, stderr := runRefusal(t, canonicalHome(t), []string{"doctor", "--target-dir", filepath.Join(cwd, existing)})
		if code != exitOK || stderr != "" || !strings.Contains(stdout, " presence=present ") {
			t.Fatalf("exit = %d stdout = %q stderr = %q", code, stdout, stderr)
		}
	})
}

func TestValidTargetDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	for _, test := range []struct {
		name   string
		target string
		want   bool
	}{
		{"absolute clean", filepath.Join(cwd, "agent-skills"), true},
		{"relative", "agent-skills", false},
		{"dot relative", "." + sep + "agent-skills", false},
		{"unclean", cwd + sep + "." + sep + "agent-skills", false},
		{"parent element", filepath.Join(cwd, "a") + sep + ".." + sep + "agent-skills", false},
		{"trailing separator", filepath.Join(cwd, "agent-skills") + sep, false},
		{"empty", "", false},
	} {
		if got := validTargetDir(test.target); got != test.want {
			t.Errorf("%s: validTargetDir(%q) = %v, want %v", test.name, test.target, got, test.want)
		}
	}
}

const invalidTargetRefusal = "error=invalid_target\n"

// runRefusal runs one CLI invocation with guarded uninstall dependencies.
func runRefusal(t *testing.T, home string, args []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(context.Background(), args, &stdout, &stderr, nil, directoryInstallDependencies(t, home), guardedUninstallDependencies(t))
	return code, stdout.String(), stderr.String()
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
