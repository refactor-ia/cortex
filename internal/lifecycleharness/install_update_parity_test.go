// Package lifecycleharness exercises the install core through isolated runtime roots.
package lifecycleharness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// The compatible observations below are synthetic fixture inputs only. They are
// structural test evidence, not real runtime compatibility or parity certification.
func TestSyntheticCompatibleInstallUpdateParity(t *testing.T) {
	home := t.TempDir()
	initial := buildPlans(t, home, "one", "alpha", "beta")
	changed := buildPlans(t, home, "two", "alpha", "beta")
	if initial[runtimematrix.RuntimePi].SnapshotFingerprint() == changed[runtimematrix.RuntimePi].SnapshotFingerprint() {
		t.Fatal("changed source did not change the snapshot")
	}

	for _, tc := range runtimeCases(home) {
		t.Run(string(tc.runtime), func(t *testing.T) {
			first, update := initial[tc.runtime], changed[tc.runtime]
			assertRuntimeShape(t, first, tc)
			mustMkdir(t, first.RootPath())

			assertActions(t, apply(t, first), first, "create")
			assertMaterialized(t, first)
			assertActions(t, apply(t, first), first, "unchanged")

			assertActions(t, apply(t, update), update, "replace")
			assertMaterialized(t, update)

			assertConflictPreservesAllFiles(t, home, tc, update)
			assertLateStateFailureRollsBackSkills(t, t.TempDir(), tc)
		})
	}
}

type runtimeCase struct {
	runtime runtimematrix.RuntimeID
	kind    skilldest.RootKind
	parts   []string
}

func runtimeCases(home string) []runtimeCase {
	return []runtimeCase{
		{runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, []string{home, ".pi", "agent"}},
		{runtimematrix.RuntimeOpenCode, skilldest.RootKindOpenCodeUserConfig, []string{home, ".config", "opencode"}},
		{runtimematrix.RuntimeClaudeCode, skilldest.RootKindClaudeCodeUser, []string{home, ".claude"}},
	}
}

func assertRuntimeShape(t *testing.T, plan installplan.Plan, tc runtimeCase) {
	t.Helper()
	expectedRoot := filepath.Join(tc.parts...)
	if plan.RuntimeID() != tc.runtime || plan.RootKind() != tc.kind || plan.RootPath() != expectedRoot {
		t.Fatal("runtime root did not use its canonical isolated destination")
	}
	files := plan.Files()
	if len(files) < 2 || files[len(files)-1].Role() != "state" {
		t.Fatal("install plan did not order canonical state last")
	}
	for _, file := range files[:len(files)-1] {
		if file.Role() != "skill" || file.RelativePath() != "skills/cortex-"+file.LogicalID()[7:]+"/SKILL.md" || file.AbsolutePath() != filepath.Join(expectedRoot, filepath.FromSlash(file.RelativePath())) {
			t.Fatal("runtime destination was not a dedicated Cortex skill artifact")
		}
	}
}

func apply(t *testing.T, plan installplan.Plan) installtxn.Result {
	t.Helper()
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	must(t, err, "observe install root")
	result, err := installtxn.Apply(plan, observation, t.TempDir(), "snapshot")
	must(t, err, "apply install plan")
	return result
}

func assertActions(t *testing.T, result installtxn.Result, plan installplan.Plan, want string) {
	t.Helper()
	actions, files := result.Actions(), plan.Files()
	if len(actions) != len(files) {
		t.Fatal("install transaction returned an incomplete action matrix")
	}
	for index, file := range files {
		if actions[index].LogicalID != file.LogicalID() || string(actions[index].Action) != want {
			t.Fatal("install transaction action did not match the expected lifecycle")
		}
	}
}

func assertMaterialized(t *testing.T, plan installplan.Plan) {
	t.Helper()
	files := plan.Files()
	if files[len(files)-1].Role() != "state" || files[len(files)-1].LogicalID() != "state/install-state" {
		t.Fatal("canonical state was not the last planned write")
	}
	for _, file := range files {
		data, err := os.ReadFile(file.AbsolutePath())
		if err != nil || !bytes.Equal(data, file.Content()) {
			t.Fatal("materialized file bytes did not match the bundle-bound plan")
		}
		info, err := os.Stat(file.AbsolutePath())
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != installplan.CanonicalFileMode {
			t.Fatal("materialized file mode was not canonical")
		}
	}
}

func assertConflictPreservesAllFiles(t *testing.T, home string, tc runtimeCase, prior installplan.Plan) {
	t.Helper()
	files := prior.Files()
	must(t, os.WriteFile(files[0].AbsolutePath(), []byte("user drift"), installplan.CanonicalFileMode), "create user drift")
	before := readFiles(t, files)
	conflict := buildPlans(t, home, "three", "alpha", "beta")[tc.runtime]
	observation, err := installobserve.Observe(conflict, installobserve.DefaultOptions())
	must(t, err, "observe user drift")
	result, err := installtxn.Apply(conflict, observation, t.TempDir(), "snapshot")
	if !errors.Is(err, installtxn.ErrConflict) {
		t.Fatal("user drift did not reject the complete transaction")
	}
	if len(result.Actions()) == 0 || string(result.Actions()[0].Action) != "conflict" {
		t.Fatal("user drift was not identified as an ownership conflict")
	}
	assertFilesUnchanged(t, before, files)
}

func assertLateStateFailureRollsBackSkills(t *testing.T, home string, tc runtimeCase) {
	t.Helper()
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = fmt.Sprintf("rollback-%02d", i)
	}
	plan := buildPlans(t, home, "rollback", ids...)[tc.runtime]
	mustMkdir(t, plan.RootPath())
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	must(t, err, "observe rollback root")
	files, state := plan.Files(), plan.Files()[len(plan.Files())-1].AbsolutePath()
	ready := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Lstat(files[0].AbsolutePath()); err == nil {
				ready <- os.Mkdir(state, 0o700)
				return
			}
			time.Sleep(time.Millisecond)
		}
		ready <- errors.New("skill write was not observed")
	}()
	_, err = installtxn.Apply(plan, observation, t.TempDir(), "snapshot")
	if raceErr := <-ready; raceErr != nil || err == nil {
		t.Fatal("late state failure did not occur")
	}
	for _, file := range files[:len(files)-1] {
		if _, err := os.Lstat(file.AbsolutePath()); !os.IsNotExist(err) {
			t.Fatal("late state failure did not roll back an earlier skill")
		}
	}
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() {
		t.Fatal("late state failure unexpectedly wrote canonical state")
	}
}

func readFiles(t *testing.T, files []installplan.File) map[string][]byte {
	t.Helper()
	values := make(map[string][]byte, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file.AbsolutePath())
		must(t, err, "capture file before conflict")
		values[file.LogicalID()] = data
	}
	return values
}

func assertFilesUnchanged(t *testing.T, want map[string][]byte, files []installplan.File) {
	t.Helper()
	for _, file := range files {
		data, err := os.ReadFile(file.AbsolutePath())
		if err != nil || !bytes.Equal(data, want[file.LogicalID()]) {
			t.Fatal("ownership conflict changed a filesystem artifact")
		}
	}
}

func buildPlans(t *testing.T, home, version string, ids ...string) map[runtimematrix.RuntimeID]installplan.Plan {
	t.Helper()
	catalogRoot := t.TempDir()
	families := make(map[string]string)
	for _, family := range catalog.ApprovedFamilyIDs() {
		paths := []string{}
		if family == "reasoning" {
			for _, id := range ids {
				paths = append(paths, "manifests/"+id+".json")
			}
		}
		families[family] = "families/" + family + ".json"
		writeJSON(t, catalogRoot, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": paths, "agents": []string{}})
		writeFile(t, catalogRoot, "routers/"+family+".md", family)
	}
	for _, id := range ids {
		writeJSON(t, catalogRoot, "manifests/"+id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: id, Family: "reasoning", Source: "sources/" + id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		writeFile(t, catalogRoot, "sources/"+id+".md", id+" "+version)
	}
	writeJSON(t, catalogRoot, "catalog.json", map[string]any{"schemaVersion": 1, "families": families})
	snapshot, err := catalog.BuildCatalogSnapshot(catalogRoot, "catalog.json", catalog.AdmissionPolicy{})
	must(t, err, "build catalog snapshot")
	sources, err := skillrender.Render(snapshot)
	must(t, err, "render catalog skills")

	runtimes := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	projected := make(map[runtimematrix.RuntimeID]skillprojection.Plan, len(runtimes))
	assessments := make([]projection.Assessment, 0, len(runtimes))
	for _, runtime := range runtimes {
		value, err := skillprojection.Build(runtime, sources)
		must(t, err, "build runtime projection")
		projected[runtime], assessments = value, append(assessments, value.Assessment())
	}
	// These observations deliberately bypass real adapter discovery and certification.
	base, err := adapterplan.Build(snapshot.Fingerprint(), syntheticCompatibleObservations())
	must(t, err, "build synthetic adapter plan")
	final, err := projection.BuildPlan(base, assessments)
	must(t, err, "build final projection plan")

	plans := make(map[runtimematrix.RuntimeID]installplan.Plan, len(runtimes))
	for _, runtime := range runtimes {
		binding, err := skillartifact.Build(projected[runtime], final)
		must(t, err, "bind projected artifacts")
		bundle, ok := binding.Bundle()
		if !ok {
			t.Fatal("representable synthetic projection did not bind a bundle")
		}
		destination, err := skilldest.Build(binding)
		must(t, err, "build skill destination")
		root, err := skillroot.Resolve(destination, skillroot.Inputs{Home: home})
		must(t, err, "resolve isolated runtime root")
		plan, err := installplan.BuildWithBundle(root, bundle)
		must(t, err, "build bundle-bound install plan")
		plans[runtime] = plan
	}
	return plans
}

func syntheticCompatibleObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "synthetic", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "synthetic", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "synthetic", Compatibility: runtimematrix.Compatible},
	}
}

func writeJSON(t *testing.T, root, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	must(t, err, "encode fixture JSON")
	writeFile(t, root, path, string(data))
}

func writeFile(t *testing.T, root, path, content string) {
	t.Helper()
	target := filepath.Join(root, path)
	mustMkdir(t, filepath.Dir(target))
	must(t, os.WriteFile(target, []byte(content), 0o600), "write fixture file")
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	must(t, os.MkdirAll(path, 0o700), "create fixture directory")
}

func must(t *testing.T, err error, action string) {
	t.Helper()
	if err != nil {
		t.Fatal(action)
	}
}
