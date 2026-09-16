package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

var updateRuntimeIDs = []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}

// updateFixture is a disposable CLI fixture: the production catalog, a patched
// copy for change scenarios, three empty runtime roots, and unrelated user
// configuration. Its normalized uncertified version reports are bounded fixture
// inputs only, never compatibility certification.
type updateFixture struct {
	home    string
	catalog string
	patched string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	mustOK(err)
	fixture := &updateFixture{home: home, catalog: "../../catalog"}
	patched := filepath.Join(t.TempDir(), "patched-catalog")
	mustOK(os.CopyFS(patched, os.DirFS(fixture.catalog)))
	source := filepath.Join(patched, "families", "quality-assurance", "sources", "requirements-analyst.md")
	mustOK(os.WriteFile(source, append(must(os.ReadFile(source)), []byte("\nPatched fixture line.\n")...), 0o600))
	fixture.patched = patched
	mustOK(os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = fixture\n"), 0o600))
	return fixture
}

func (fixture *updateFixture) dependencies(target runtimematrix.RuntimeID) updateDependencies {
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, id := range updateRuntimeIDs {
		if id == target {
			observations = append(observations, runtimematrix.Observation{ID: id, Present: true, Version: "9.9.9", Compatibility: runtimematrix.CompatibilityUnknown})
			continue
		}
		observations = append(observations, runtimematrix.Observation{ID: id, Present: false, Compatibility: runtimematrix.CompatibilityUnknown})
	}
	return updateDependencies{
		compatibility: func([]runtimeprobe.Report) ([]runtimematrix.Observation, error) { return observations, nil },
		resolveInputs: func() (skillroot.Inputs, error) { return skillroot.Inputs{Home: fixture.home}, nil },
	}
}

func runUpdateCommand(t *testing.T, deps updateDependencies, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runWithUpdateDependencies(context.Background(), args, stdout, stderr, readyRunner(), deps)
	return code, stdout.String(), stderr.String()
}

func updateRootPath(t *testing.T, fixture *updateFixture, id runtimematrix.RuntimeID) string {
	t.Helper()
	for _, root := range must(skillroot.ResolveUninstallRoots(skillroot.Inputs{Home: fixture.home})) {
		if root.RuntimeID() == id {
			mustOK(os.MkdirAll(root.RootPath(), 0o700))
			return root.RootPath()
		}
	}
	t.Fatal("missing fixture root")
	return ""
}

// updateStateIdentity decodes the installed state and verifies every declared
// artifact exists with its exact canonical bytes and mode.
func updateStateIdentity(t *testing.T, root string) installstate.Manifest {
	t.Helper()
	manifest := must(installstate.Decode(must(os.ReadFile(filepath.Join(root, ".cortex", "install-state.json")))))
	for _, artifact := range manifest.Artifacts() {
		path := filepath.Join(root, filepath.FromSlash(artifact.RelativePath()))
		sum := sha256.Sum256(must(os.ReadFile(path)))
		if hex.EncodeToString(sum[:]) != artifact.SHA256() || must(os.Stat(path)).Mode().Perm() != installplan.CanonicalFileMode {
			t.Fatalf("artifact %q does not match its installed state identity", artifact.LogicalID())
		}
	}
	return manifest
}

// updateUnchanged asserts untouched runtime roots stayed empty and the
// unrelated user config kept its exact bytes and mode.
func updateUnchanged(t *testing.T, fixture *updateFixture, target runtimematrix.RuntimeID, config string, mode fs.FileMode) {
	t.Helper()
	for _, id := range updateRuntimeIDs {
		if id == target {
			continue
		}
		root := updateRootPath(t, fixture, id)
		if entries := must(os.ReadDir(root)); len(entries) != 0 {
			t.Fatalf("%s root gained %d entries without an update", id, len(entries))
		}
	}
	data := must(os.ReadFile(filepath.Join(fixture.home, ".gitconfig")))
	info := must(os.Stat(filepath.Join(fixture.home, ".gitconfig")))
	if string(data) != config || info.Mode() != mode {
		t.Fatal("unrelated user configuration changed")
	}
}

// buildPiSkillsPlan builds the canonical v1 skill-only Pi candidate from the
// production catalog; it is fixture evidence for a prior v1 installation.
func buildPiSkillsPlan(t *testing.T, catalogDir, home string) installplan.Plan {
	t.Helper()
	snapshot := must(catalog.BuildCatalogSnapshot(catalogDir, "catalog.json", catalog.AdmissionPolicy{}))
	sources := must(skillrender.Render(snapshot))
	assessments := make([]projection.Assessment, 0, 3)
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, id := range updateRuntimeIDs {
		projected := must(skillprojection.Build(id, sources))
		assessments = append(assessments, projected.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: id, Present: true, Version: "fixture", Compatibility: runtimematrix.Compatible})
	}
	final := must(projection.BuildPlan(must(adapterplan.Build(snapshot.Fingerprint(), observations)), assessments))
	binding := must(skillartifact.Build(must(skillprojection.Build(runtimematrix.RuntimePi, sources)), final))
	bundle, bound := binding.Bundle()
	if !bound {
		t.Fatal("missing bundle")
	}
	resolved := must(skillroot.Resolve(must(skilldest.Build(binding)), skillroot.Inputs{Home: home}))
	return must(installplan.BuildWithBundle(resolved, bundle))
}

func TestUpdatePreviewAndApplyPerRuntime(t *testing.T) {
	for _, target := range updateRuntimeIDs {
		t.Run(string(target), func(t *testing.T) {
			fixture := newUpdateFixture(t)
			root := updateRootPath(t, fixture, target)
			config := must(os.ReadFile(filepath.Join(fixture.home, ".gitconfig")))
			configMode := must(os.Stat(filepath.Join(fixture.home, ".gitconfig"))).Mode()
			code, stdout, stderr := runUpdateCommand(t, fixture.dependencies(target), "update", "--runtime", string(target), "--catalog", fixture.catalog)
			if code != exitOK || stderr != "" || !strings.Contains(stdout, "mode=plan") || !strings.Contains(stdout, "status=planned") || !strings.Contains(stdout, "warning=uncertified_observed_version version=9.9.9 certification=not_certified") {
				t.Fatalf("preview = (%d, %q, %q)", code, stdout, stderr)
			}
			if entries := must(os.ReadDir(root)); len(entries) != 0 {
				t.Fatalf("preview wrote %d entries into the runtime root", len(entries))
			}
			updateUnchanged(t, fixture, target, string(config), configMode)
			code, stdout, stderr = runUpdateCommand(t, fixture.dependencies(target), "update", "--runtime", string(target), "--catalog", fixture.catalog, "--apply")
			if code != exitOK || stderr != "" || !strings.Contains(stdout, "status=applied") || !strings.Contains(stdout, "backup=install-") || !strings.Contains(stdout, "warning=uncertified_observed_version version=9.9.9 certification=not_certified") {
				t.Fatalf("apply = (%d, %q, %q)", code, stdout, stderr)
			}
			manifest := updateStateIdentity(t, root)
			if target == runtimematrix.RuntimePi && (manifest.SchemaVersion() != 2 || len(manifest.Artifacts()) != 12) {
				t.Fatalf("Pi state = v%d with %d artifacts, want canonical v2 actors and skills", manifest.SchemaVersion(), len(manifest.Artifacts()))
			}
			if target != runtimematrix.RuntimePi && (manifest.SchemaVersion() != 1 || len(manifest.Artifacts()) != 6) {
				t.Fatalf("native state = v%d with %d artifacts, want canonical v1 skills", manifest.SchemaVersion(), len(manifest.Artifacts()))
			}
			updateUnchanged(t, fixture, target, string(config), configMode)
		})
	}
}

func TestUpdatePriorV1PiUpgradesToCanonicalV2(t *testing.T) {
	fixture := newUpdateFixture(t)
	root := updateRootPath(t, fixture, runtimematrix.RuntimePi)
	prior := buildPiSkillsPlan(t, fixture.catalog, fixture.home)
	observation := must(installobserve.Observe(prior, installobserve.DefaultOptions()))
	mustOK(func() error { _, err := installtxn.Apply(prior, observation, t.TempDir(), "prior"); return err }())
	if manifest := updateStateIdentity(t, root); manifest.SchemaVersion() != 1 {
		t.Fatal("prior installation is not v1")
	}
	config := must(os.ReadFile(filepath.Join(fixture.home, ".gitconfig")))
	configMode := must(os.Stat(filepath.Join(fixture.home, ".gitconfig"))).Mode()
	deps := fixture.dependencies(runtimematrix.RuntimePi)
	code, stdout, stderr := runUpdateCommand(t, deps, "update", "--runtime", "pi", "--catalog", fixture.catalog)
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "status=planned") || !strings.Contains(stdout, "operation=actors/requirements-analyst action=create") {
		t.Fatalf("pi preview = (%d, %q, %q)", code, stdout, stderr)
	}
	updateStateIdentity(t, root)
	code, stdout, stderr = runUpdateCommand(t, deps, "update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply")
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "status=applied") || !strings.Contains(stdout, "transaction=") || !strings.Contains(stdout, "backup=install-") {
		t.Fatalf("pi apply = (%d, %q, %q)", code, stdout, stderr)
	}
	manifest := updateStateIdentity(t, root)
	if manifest.SchemaVersion() != 2 || len(manifest.Artifacts()) != 12 {
		t.Fatalf("pi state = v%d with %d artifacts, want canonical v2 with six actors and six skills", manifest.SchemaVersion(), len(manifest.Artifacts()))
	}
	binding := must(qapi.CatalogAdmissionBinding(must(catalog.BuildCatalogSnapshot(fixture.catalog, "catalog.json", catalog.AdmissionPolicy{})), qarole.RequirementsAnalyst, "pi"))
	assets := must(installobserve.ObserveAdmissionAssets(root, updateCWD(), binding))
	if _, err := os.Stat(assets.ActorPath()); err != nil {
		t.Fatalf("admission actor asset missing: %v", err)
	}
	updateUnchanged(t, fixture, runtimematrix.RuntimePi, string(config), configMode)
}

func updateCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return cwd
}

func TestUpdateRejectsDriftedOwnedFileWithoutWrites(t *testing.T) {
	fixture := newUpdateFixture(t)
	root := updateRootPath(t, fixture, runtimematrix.RuntimeOpenCode)
	code, stdout, stderr := runUpdateCommand(t, fixture.dependencies(runtimematrix.RuntimeOpenCode), "update", "--runtime", "opencode", "--catalog", fixture.catalog, "--apply")
	if code != exitOK || stderr != "" {
		t.Fatalf("prior apply = (%d, %q, %q)", code, stdout, stderr)
	}
	manifest := updateStateIdentity(t, root)
	statePath := filepath.Join(root, ".cortex", "install-state.json")
	stateBefore := must(os.ReadFile(statePath))
	drifted := filepath.Join(root, filepath.FromSlash(manifest.Artifacts()[0].RelativePath()))
	mustOK(os.WriteFile(drifted, []byte("user-overwrite"), 0o600))
	for _, apply := range []bool{false, true} {
		args := []string{"update", "--runtime", "opencode", "--catalog", fixture.catalog}
		if apply {
			args = append(args, "--apply")
		}
		code, stdout, stderr := runUpdateCommand(t, fixture.dependencies(runtimematrix.RuntimeOpenCode), args...)
		if code != exitConflict || !strings.Contains(stdout, "status=conflict") {
			t.Fatalf("drifted update (apply=%t) = (%d, %q, %q)", apply, code, stdout, stderr)
		}
		if string(must(os.ReadFile(statePath))) != string(stateBefore) || string(must(os.ReadFile(drifted))) != "user-overwrite" {
			t.Fatal("drifted root changed without authorization")
		}
	}
}

func TestUpdateInjectedFailureAfterWriteRollsBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mid-write failure injection requires a non-root user")
	}
	fixture := newUpdateFixture(t)
	root := updateRootPath(t, fixture, runtimematrix.RuntimeOpenCode)
	code, stdout, stderr := runUpdateCommand(t, fixture.dependencies(runtimematrix.RuntimeOpenCode), "update", "--runtime", "opencode", "--catalog", fixture.catalog, "--apply")
	if code != exitOK || stderr != "" {
		t.Fatalf("prior apply = (%d, %q, %q)", code, stdout, stderr)
	}
	statePath := filepath.Join(root, ".cortex", "install-state.json")
	stateBefore := string(must(os.ReadFile(statePath)))
	blocked := filepath.Join(root, ".cortex")
	mustOK(os.Chmod(blocked, 0o500))
	t.Cleanup(func() { mustOK(os.Chmod(blocked, 0o700)) })
	code, stdout, stderr = runUpdateCommand(t, fixture.dependencies(runtimematrix.RuntimeOpenCode), "update", "--runtime", "opencode", "--catalog", fixture.patched, "--apply")
	if code != exitTransaction || !strings.Contains(stderr, "error=update_transaction_failed") {
		t.Fatalf("injected failure = (%d, %q, %q)", code, stdout, stderr)
	}
	if string(must(os.ReadFile(statePath))) != stateBefore {
		t.Fatal("state file changed after a failed transaction")
	}
	updateStateIdentity(t, root)
	entries := must(os.ReadDir(blocked))
	if len(entries) != 2 {
		t.Fatalf("rollback evidence backup was not retained: %d entries", len(entries))
	}
}

func TestUpdateRefusalsLeaveFixturesUnchanged(t *testing.T) {
	fixture := newUpdateFixture(t)
	root := updateRootPath(t, fixture, runtimematrix.RuntimePi)
	before := must(os.ReadFile(filepath.Join(fixture.home, ".gitconfig")))
	beforeMode := must(os.Stat(filepath.Join(fixture.home, ".gitconfig"))).Mode()
	assertNoWrites := func() {
		t.Helper()
		if entries := must(os.ReadDir(root)); len(entries) != 0 {
			t.Fatalf("refusal wrote %d root entries", len(entries))
		}
		if got := must(os.ReadFile(filepath.Join(fixture.home, ".gitconfig"))); !bytes.Equal(got, before) || must(os.Stat(filepath.Join(fixture.home, ".gitconfig"))).Mode() != beforeMode {
			t.Fatal("refusal changed unrelated configuration")
		}
	}
	unknown := updateDependencies{
		compatibility: func([]runtimeprobe.Report) ([]runtimematrix.Observation, error) {
			return []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			}, nil
		},
		resolveInputs: fixture.dependencies(runtimematrix.RuntimePi).resolveInputs,
	}
	code, output, _ := runUpdateCommand(t, unknown, "update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply")
	if code != exitUnknown || !strings.Contains(output, "reason=compatibility_uncertified") {
		t.Fatalf("missing-version apply = (%d, %q)", code, output)
	}
	assertNoWrites()
	code, output, _ = runUpdateCommand(t, unknown, "update", "--runtime", "opencode", "--catalog", fixture.catalog)
	if code != exitUnknown || !strings.Contains(output, "reason=runtime_absent") {
		t.Fatalf("absent target = (%d, %q)", code, output)
	}
	assertNoWrites()
	incompatible := updateDependencies{
		compatibility: func([]runtimeprobe.Report) ([]runtimematrix.Observation, error) {
			return []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: true, Version: "9.9.9", Compatibility: runtimematrix.Incompatible},
				{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			}, nil
		},
		resolveInputs: unknown.resolveInputs,
	}
	code, output, _ = runUpdateCommand(t, incompatible, "update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply")
	if code != exitUnknown || !strings.Contains(output, "reason=runtime_known_incompatible") {
		t.Fatalf("known-incompatible apply = (%d, %q)", code, output)
	}
	assertNoWrites()
	unparseable := readyRunner()
	unparseable.runs["/private/pi"] = fakeRun{execution: runtimeprobe.Execution{Stdout: []byte("not a version")}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code = runWithUpdateDependencies(context.Background(), []string{"update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply"}, stdout, stderr, unparseable, updateDependencies{compatibility: runtimecompat.BuiltInPolicy().Evaluate, resolveInputs: unknown.resolveInputs})
	if code != exitUnknown || !strings.Contains(stdout.String(), "reason=compatibility_uncertified") || stderr.Len() != 0 {
		t.Fatalf("unparseable probe = (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
	assertNoWrites()
	failed := readyRunner()
	failed.lookup = map[string]error{"pi": errors.New("fixture lookup failed")}
	stdout.Reset()
	stderr.Reset()
	code = runWithUpdateDependencies(context.Background(), []string{"update", "--runtime", "pi", "--catalog", fixture.catalog, "--apply"}, stdout, stderr, failed, unknown)
	if code != exitFailure || stdout.Len() != 0 || stderr.String() != "error=probe_failed\n" {
		t.Fatalf("probe failure = (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
	assertNoWrites()
}

func TestUpdateUsageArguments(t *testing.T) {
	fixture := newUpdateFixture(t)
	deps := fixture.dependencies(runtimematrix.RuntimePi)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing runtime", []string{"update", "--catalog", fixture.catalog}},
		{"unknown runtime", []string{"update", "--runtime", "cursor", "--catalog", fixture.catalog}},
		{"missing catalog", []string{"update", "--runtime", "pi"}},
		{"duplicate runtime", []string{"update", "--runtime", "pi", "--runtime", "opencode", "--catalog", fixture.catalog}},
		{"stray argument", []string{"update", "--runtime", "pi", "--catalog", fixture.catalog, "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runUpdateCommand(t, deps, tc.args...)
			if code != exitUsage || stdout != "" || stderr != "error=invalid_command\n" {
				t.Fatalf("%s = (%d, %q, %q)", tc.name, code, stdout, stderr)
			}
		})
	}
}

// TestRunDispatchesExplicitUpdateArguments proves the top-level dispatcher
// routes an argument-bearing update to the single-runtime command instead of
// rejecting it as a bare-update flag; bare update keeps its transaction path.
func TestRunDispatchesExplicitUpdateArguments(t *testing.T) {
	fixture := newUpdateFixture(t)
	t.Setenv("HOME", fixture.home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	updateRootPath(t, fixture, runtimematrix.RuntimePi)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"update", "--runtime", "pi", "--catalog", fixture.catalog}, &stdout, &stderr, readyRunner())
	if code == exitUsage || strings.Contains(stderr.String(), "invalid_command") {
		t.Fatalf("explicit update was not dispatched: (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "runtime=pi") {
		t.Fatalf("explicit update output = %q, want runtime=pi report", stdout.String())
	}
}
