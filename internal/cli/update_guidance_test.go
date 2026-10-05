package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

var updateGuidanceSuffix = []byte("\n\n<!-- gentle-ai:pi-codegraph-guidance -->\nexternal guidance\r\n\t  \n<!-- /gentle-ai:pi-codegraph -->\n")

func updateGuidanceTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	mustOK(filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info := must(entry.Info())
		tree[path] = info.Mode().String()
		if info.Mode().IsRegular() {
			tree[path] += string(must(os.ReadFile(path)))
		}
		return nil
	}))
	return tree
}

func TestUpdateGuidanceLifecycle(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.catalog, fixture.patched = must(filepath.Abs(fixture.catalog)), must(filepath.Abs(fixture.patched))
	t.Chdir(must(filepath.EvalSymlinks(t.TempDir())))
	root, deps := updateRootPath(t, fixture, runtimematrix.RuntimePi), fixture.dependencies(runtimematrix.RuntimePi)
	run := func(catalog string, flags ...string) string {
		t.Helper()
		args := append([]string{"update", "--runtime", "pi", "--catalog", catalog}, flags...)
		code, out, err := runUpdateCommand(t, deps, args...)
		if code != exitOK || err != "" {
			t.Fatalf("%v = (%d, %q, %q)", args, code, out, err)
		}
		return out
	}
	run(fixture.catalog, "--apply")
	prior := updateStateIdentity(t, root)
	// Opt-in on an ordinary no-guidance v2 installation must not migrate it.
	run(fixture.catalog, "--adopt-guidance", "--apply")
	if updateStateIdentity(t, root).SchemaVersion() != 2 {
		t.Fatal("ordinary v2 was migrated")
	}
	for _, a := range prior.Artifacts() {
		if a.Kind() == installstate.KindPiActor {
			path := filepath.Join(root, filepath.FromSlash(a.RelativePath()))
			mustOK(os.WriteFile(path, append(bytes.TrimRight(must(os.ReadFile(path)), "\n"), updateGuidanceSuffix...), 0o600))
		}
	}
	before := updateGuidanceTree(t, fixture.home)
	for _, apply := range []bool{false, true} {
		args := []string{"update", "--runtime", "pi", "--catalog", fixture.patched}
		if apply {
			args = append(args, "--apply")
		}
		code, out, err := runUpdateCommand(t, deps, args...)
		if code != exitConflict || err != "" || !strings.Contains(out, "status=conflict") {
			t.Fatalf("no opt-in = (%d, %q, %q)", code, out, err)
		}
		if !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
			t.Fatal("refused guidance update wrote files")
		}
	}
	out := run(fixture.patched, "--adopt-guidance")
	if !strings.Contains(out, "mode=plan") || !strings.Contains(out, "status=planned") || !strings.Contains(out, "operation=actors/requirements-analyst action=replace") || !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
		t.Fatalf("explicit plan changed files or omitted composed action: %q", out)
	}
	out = run(fixture.patched, "--adopt-guidance", "--apply")
	if !strings.Contains(out, "transaction=") || !strings.Contains(out, "backup=install-") || !strings.Contains(out, "status=applied") {
		t.Fatalf("missing transaction report: %q", out)
	}
	state := updateStateIdentity(t, root) // Checks full effective hashes on disk.
	if state.SchemaVersion() != 3 || state.InstallationID() != prior.InstallationID() {
		t.Fatal("composed state lost installation identity")
	}
	for _, a := range state.Artifacts() {
		if a.Kind() == installstate.KindPiActor && (!bytes.HasSuffix(must(os.ReadFile(filepath.Join(root, filepath.FromSlash(a.RelativePath())))), updateGuidanceSuffix) || a.CanonicalSHA256() == a.SHA256()) {
			t.Fatal("suffix lost or effective identity reported as canonical")
		}
	}
	before = updateGuidanceTree(t, fixture.home)
	for _, flags := range [][]string{nil, {"--apply"}} {
		out = run(fixture.patched, flags...)
		if strings.Contains(out, "backup=") || strings.Contains(out, "transaction=") || !strings.Contains(out, "operation=state/install-state action=unchanged") || !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
			t.Fatalf("registered v3 repeat was not a no-op: %q", out)
		}
	}
	// A registered v3 installation can also change canonical content without
	// adopting again, keeping both identities and the external bytes intact.
	out = run(fixture.catalog, "--apply")
	state = updateStateIdentity(t, root)
	if state.SchemaVersion() != 3 || state.InstallationID() != prior.InstallationID() || !strings.Contains(out, "operation=actors/requirements-analyst action=replace") {
		t.Fatalf("registered v3 update failed: %q", out)
	}
	canonical := must(buildUpdateCandidate(runtimematrix.RuntimePi, fixture.catalog, must(deps.compatibility(nil)), deps))
	for _, a := range state.Artifacts() {
		for _, f := range canonical.Files() {
			if a.Kind() == installstate.KindPiActor && a.LogicalID() == f.LogicalID() && (a.CanonicalSHA256() != f.SHA256() || !bytes.HasSuffix(must(os.ReadFile(f.AbsolutePath())), updateGuidanceSuffix)) {
				t.Fatal("registered update lost canonical hash or suffix")
			}
		}
	}
	// Registered effective drift cannot be overridden, even with adoption.
	for _, a := range state.Artifacts() {
		if a.Kind() == installstate.KindPiActor {
			path := filepath.Join(root, filepath.FromSlash(a.RelativePath()))
			mustOK(os.WriteFile(path, append(must(os.ReadFile(path)), []byte("drift")...), 0o600))
			break
		}
	}
	before = updateGuidanceTree(t, fixture.home)
	for _, flags := range [][]string{nil, {"--apply"}, {"--adopt-guidance"}, {"--adopt-guidance", "--apply"}} {
		code, out, err := runUpdateCommand(t, deps, append([]string{"update", "--runtime", "pi", "--catalog", fixture.catalog}, flags...)...)
		if code != exitConflict || err != "" || !strings.Contains(out, "status=conflict") || !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
			t.Fatalf("drift refusal = (%d, %q, %q)", code, out, err)
		}
	}
}

func TestUpdateGuidanceProductionApplyRechecks(t *testing.T) {
	for _, scenario := range []string{"actor", "skill", "state", "shadow"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			fixture.catalog = must(filepath.Abs(fixture.catalog))
			cwd := must(filepath.EvalSymlinks(t.TempDir()))
			t.Chdir(cwd)
			root, deps := updateRootPath(t, fixture, runtimematrix.RuntimePi), fixture.dependencies(runtimematrix.RuntimePi)
			code, out, err := runUpdateCommand(t, deps, "update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply")
			if code != exitOK || err != "" {
				t.Fatalf("seed = (%d, %q, %q)", code, out, err)
			}
			observations := must(deps.compatibility(nil))
			canonical := must(buildUpdateCandidate(runtimematrix.RuntimePi, fixture.catalog, observations, deps))
			for _, f := range canonical.Files() {
				if f.Role() == "actor" {
					mustOK(os.WriteFile(f.AbsolutePath(), append(bytes.TrimRight(f.Content(), "\n"), updateGuidanceSuffix...), 0o600))
				}
			}
			observation := must(installobserve.Observe(canonical, installobserve.DefaultOptions()))
			preflight := must(installcoord.PreflightActorGuidance(observations, installcoord.Unit{Plan: canonical, Observation: observation}, true, cwd))
			if scenario == "shadow" {
				path := filepath.Join(cwd, ".pi", "subagents", "shadow.md")
				mustOK(os.MkdirAll(filepath.Dir(path), 0o700))
				mustOK(os.WriteFile(path, []byte("---\nname: cortex-test-designer\n---\n"), 0o600))
			} else {
				for _, f := range canonical.Files() {
					if f.Role() == scenario {
						mustOK(os.WriteFile(f.AbsolutePath(), []byte("stale"), 0o600))
						break
					}
				}
			}
			before, project := updateGuidanceTree(t, fixture.home), updateGuidanceTree(t, cwd)
			_, applyErr := installtxn.ApplyActorGuidance(canonical, observation, preflight.Proof(), cwd, filepath.Join(root, ".cortex"), "refused")
			if applyErr == nil || !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) || !reflect.DeepEqual(project, updateGuidanceTree(t, cwd)) {
				t.Fatalf("fresh %s check failed: %v", scenario, applyErr)
			}
		})
	}
}

func TestUpdateGuidanceFlagsAndHelp(t *testing.T) {
	fixture := newUpdateFixture(t)
	for _, target := range []string{"opencode", "claude-code", "pi"} {
		t.Run(target, func(t *testing.T) {
			args := []string{"update", "--runtime", target, "--catalog", fixture.catalog, "--adopt-guidance"}
			if target == "pi" {
				args = append(args, "--adopt-guidance")
			}
			before := updateGuidanceTree(t, fixture.home)
			code, out, err := runUpdateCommand(t, fixture.dependencies(runtimematrix.RuntimePi), args...)
			if code != exitUsage || out != "" || err != "error=invalid_command\n" || !reflect.DeepEqual(before, updateGuidanceTree(t, fixture.home)) {
				t.Fatalf("invalid flag = (%d, %q, %q)", code, out, err)
			}
		})
	}
	code, out, err := runUpdateCommand(t, updateDependencies{}, "update", "--help")
	if code != exitOK || err != "" {
		t.Fatalf("help = (%d, %q, %q)", code, out, err)
	}
	for _, text := range []string{"--adopt-guidance", "Pi-only", "unsupported", "uninstall", "read-only"} {
		if !strings.Contains(out, text) {
			t.Fatalf("help omitted %q: %q", text, out)
		}
	}
}
