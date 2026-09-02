package installtxn

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

func TestApplyGroupRuntimeHarness(t *testing.T) {
	home := t.TempDir()
	plans := groupCandidates(t, home, "one")
	result := applyGroup(t, plans...)
	if got := result.RuntimeIDs(); len(got) != 3 || got[0] != runtimematrix.RuntimePi || got[2] != runtimematrix.RuntimeClaudeCode || result.Counts().Create != 9 {
		t.Fatalf("fresh ApplyGroup() = (%#v, %#v)", got, result.Counts())
	}
	for _, plan := range plans {
		assertFile(t, plan.Files()[len(plan.Files())-1], true)
	}
	if result := applyGroup(t, plans...); result.Counts().Unchanged != 9 {
		t.Fatalf("idempotent counts = %#v", result.Counts())
	}
	changed := groupCandidates(t, home, "two")
	if result := applyGroup(t, changed...); result.Counts().Replace != 9 {
		t.Fatalf("changed counts = %#v", result.Counts())
	}
}

func TestApplyGroupCreatesAbsentRoots(t *testing.T) {
	home := t.TempDir()
	plans := absentGroupCandidates(t, home, "one")
	requests := groupRequests(t, plans...)
	calls := 0
	result, err := applyGroupWith(requests, home, ".cortex-backup", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation) (filetxn.Snapshot, error) {
		calls++
		state := false
		for _, operation := range operations {
			path := ""
			if operation.Create != nil {
				path = operation.Create.Path
			}
			if filepath.Base(path) == "install-state.json" {
				state = true
			} else if state {
				t.Fatalf("skill operation followed state: %#v", operation)
			}
		}
		mustCreate := map[string]bool{".pi": true, ".pi/agent": true, ".config": true, ".config/opencode": true, ".claude": true}
		for _, directory := range directories {
			if mustCreate[directory.Path] {
				if !directory.MustCreate {
					t.Fatalf("absent-root ancestor is not must-create: %#v", directory)
				}
				delete(mustCreate, directory.Path)
			}
		}
		if len(mustCreate) != 0 {
			t.Fatalf("missing must-create ancestors: %#v", mustCreate)
		}
		return filetxn.ApplyOperationsWithDirectories(root, backupRoot, backupName, directories, operations)
	})
	must(t, err)
	if calls != 1 || result.Counts().Create != 9 {
		t.Fatalf("ApplyGroup() = (%d, %#v)", calls, result.Counts())
	}
	for _, plan := range plans {
		for _, file := range plan.Files() {
			assertFile(t, file, true)
		}
	}
	if info, err := os.Lstat(filepath.Join(home, ".cortex-backup")); err != nil || !info.IsDir() {
		t.Fatalf("backup = (%v, %v)", info, err)
	}
}

func TestApplyGroupCreatesSingletonAndMixedAbsentRoots(t *testing.T) {
	t.Run("singleton", func(t *testing.T) {
		home := t.TempDir()
		plan := absentGroupCandidates(t, home, "one")[0]
		result, err := ApplyGroup(groupRequests(t, plan), home, ".cortex-backup")
		must(t, err)
		if got := result.RuntimeIDs(); len(got) != 1 || got[0] != plan.RuntimeID() {
			t.Fatalf("RuntimeIDs() = %#v", got)
		}
		for _, file := range plan.Files() {
			assertFile(t, file, true)
		}
	})
	t.Run("mixed", func(t *testing.T) {
		home := t.TempDir()
		plans := absentGroupCandidates(t, home, "one")
		mustMkdir(t, plans[1].RootPath())
		_, err := ApplyGroup(groupRequests(t, plans...), home, ".cortex-backup")
		must(t, err)
	})
}

func TestApplyGroupRejectsAbsentRootChanges(t *testing.T) {
	t.Run("before re-observation", func(t *testing.T) {
		home := t.TempDir()
		plan := absentGroupCandidates(t, home, "one")[0]
		request := groupRequests(t, plan)[0]
		mustMkdir(t, plan.RootPath())
		called := false
		_, err := applyGroupWith([]GroupRequest{request}, home, ".cortex-backup", func(string, string, string, []filetxn.Directory, []filetxn.Operation) (filetxn.Snapshot, error) {
			called = true
			return filetxn.Snapshot{}, nil
		})
		if !errors.Is(err, ErrInvalid) || called {
			t.Fatalf("ApplyGroup() = (%v, %t)", err, called)
		}
	})
	t.Run("after re-observation", func(t *testing.T) {
		home := t.TempDir()
		plan := absentGroupCandidates(t, home, "one")[0]
		_, err := applyGroupWith(groupRequests(t, plan), home, ".cortex-backup", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation) (filetxn.Snapshot, error) {
			mustMkdir(t, plan.RootPath())
			return filetxn.ApplyOperationsWithDirectories(root, backupRoot, backupName, directories, operations)
		})
		if !errors.Is(err, ErrFailed) {
			t.Fatalf("ApplyGroup() error = %v", err)
		}
		for _, file := range plan.Files() {
			assertFile(t, file, false)
		}
	})
}

func TestApplyGroupRejectsConflictBeforeMutation(t *testing.T) {
	home := t.TempDir()
	plans := groupCandidates(t, home, "one")
	must(t, os.MkdirAll(filepath.Dir(plans[1].Files()[0].AbsolutePath()), 0o700))
	must(t, os.WriteFile(plans[1].Files()[0].AbsolutePath(), []byte("user"), 0o600))
	_, err := applyGroupRequest(t, plans...)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyGroup() error = %v", err)
	}
	for index, plan := range plans {
		for _, file := range plan.Files() {
			if index == 1 && file.LogicalID() == plans[1].Files()[0].LogicalID() {
				continue
			}
			assertFile(t, file, false)
		}
	}
	if data, err := os.ReadFile(plans[1].Files()[0].AbsolutePath()); err != nil || string(data) != "user" {
		t.Fatalf("conflict mutated: %v", err)
	}
}

func TestApplyGroupRejectsInjectedTransactionFailureAndInvalidGroups(t *testing.T) {
	home := t.TempDir()
	plans := groupCandidates(t, home, "one")
	requests := groupRequests(t, plans...)
	expectedDirectories := []filetxn.Directory{
		{Path: ".claude/.cortex", Mode: 0o700},
		{Path: ".claude/skills", Mode: 0o700},
		{Path: ".claude/skills/cortex-alpha", Mode: 0o700},
		{Path: ".claude/skills/cortex-beta", Mode: 0o700},
		{Path: ".config/opencode/.cortex", Mode: 0o700},
		{Path: ".config/opencode/skills", Mode: 0o700},
		{Path: ".pi/agent/.cortex", Mode: 0o700},
		{Path: ".pi/agent/skills", Mode: 0o700},
		{Path: ".config/opencode/skills/cortex-alpha", Mode: 0o700},
		{Path: ".config/opencode/skills/cortex-beta", Mode: 0o700},
		{Path: ".pi/agent/skills/cortex-alpha", Mode: 0o700},
		{Path: ".pi/agent/skills/cortex-beta", Mode: 0o700},
	}
	expectedOperations := groupOperations(t, home, plans)
	calls := 0
	_, err := applyGroupWith(requests, t.TempDir(), "snapshot", func(root, _, _ string, directories []filetxn.Directory, operations []filetxn.Operation) (filetxn.Snapshot, error) {
		calls++
		if root != home {
			t.Errorf("transaction root = %q, want %q", root, home)
		}
		if !reflect.DeepEqual(directories, expectedDirectories) {
			t.Errorf("directories = %#v, want %#v", directories, expectedDirectories)
		}
		if !reflect.DeepEqual(operations, expectedOperations) {
			t.Errorf("operations = %#v, want %#v", operations, expectedOperations)
		}
		return filetxn.Snapshot{}, errors.New("injected transaction failure")
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("ApplyGroup() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("grouped apply calls = %d, want 1", calls)
	}
	for _, plan := range plans {
		for _, file := range plan.Files() {
			assertFile(t, file, false)
		}
	}
	validPlans := groupCandidates(t, t.TempDir(), "one")
	observations := groupRequests(t, validPlans...)
	if _, err := ApplyGroup([]GroupRequest{observations[1], observations[0]}, t.TempDir(), "order"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out of order error = %v", err)
	}
	if _, err := ApplyGroup([]GroupRequest{observations[0], observations[0]}, t.TempDir(), "root"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate root error = %v", err)
	}
	changed := groupCandidates(t, filepath.Dir(filepath.Dir(validPlans[0].RootPath())), "two")
	changedRequests := groupRequests(t, changed...)
	if _, err := ApplyGroup([]GroupRequest{observations[0], changedRequests[1]}, t.TempDir(), "snapshot"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed snapshot error = %v", err)
	}
}

func TestApplyRuntimeHarness(t *testing.T) {
	home := t.TempDir()
	old := candidate(t, home, "one", "alpha", "beta")
	mustMkdir(t, old.RootPath())
	apply(t, old)
	assertFile(t, old.Files()[0], true)
	assertFile(t, old.Files()[1], true)
	assertFile(t, old.Files()[2], true)
	if mode(t, filepath.Join(old.RootPath(), "skills")) != 0o700 || mode(t, filepath.Join(old.RootPath(), ".cortex")) != 0o700 {
		t.Fatal("fresh install did not create conservative parent directories")
	}

	unchanged := apply(t, old)
	if !hasAction(unchanged, "skills/alpha", "unchanged") || !hasAction(unchanged, "state/install-state", "unchanged") {
		t.Fatalf("idempotent update actions = %#v", unchanged.Actions())
	}

	changed := candidate(t, home, "two", "alpha", "beta")
	result := apply(t, changed)
	if !hasAction(result, "skills/alpha", "replace") || !hasAction(result, "state/install-state", "replace") {
		t.Fatalf("changed update actions = %#v", result.Actions())
	}
	assertFile(t, changed.Files()[0], true)

	removed := candidate(t, home, "two", "alpha")
	result = apply(t, removed)
	if !hasAction(result, "skills/beta", "remove") {
		t.Fatalf("remove actions = %#v", result.Actions())
	}
	assertFile(t, old.Files()[1], false)
}

func TestApplyRejectsGlobalConflictBeforeMutation(t *testing.T) {
	plan := candidate(t, t.TempDir(), "one", "alpha")
	mustMkdir(t, filepath.Dir(plan.Files()[0].AbsolutePath()))
	if err := os.WriteFile(plan.Files()[0].AbsolutePath(), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(plan, observation, t.TempDir(), "snapshot")
	if !errors.Is(err, ErrConflict) || !hasAction(result, "skills/alpha", "conflict") {
		t.Fatalf("Apply() = (%#v, %v)", result, err)
	}
	data, readErr := os.ReadFile(plan.Files()[0].AbsolutePath())
	if readErr != nil || string(data) != "user" {
		t.Fatalf("conflict mutated skill: %q, %v", data, readErr)
	}
	if _, err := os.Lstat(filepath.Join(plan.RootPath(), ".cortex")); !os.IsNotExist(err) {
		t.Fatal("conflict created a Cortex directory")
	}
}

func TestApplyRejectsStaleObservation(t *testing.T) {
	plan := candidate(t, t.TempDir(), "one", "alpha")
	mustMkdir(t, filepath.Dir(plan.Files()[0].AbsolutePath()))
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	must(t, err)
	must(t, os.WriteFile(plan.Files()[0].AbsolutePath(), []byte("user"), 0o600))
	_, err = Apply(plan, observation, t.TempDir(), "snapshot")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Apply() error = %v", err)
	}
	data, err := os.ReadFile(plan.Files()[0].AbsolutePath())
	must(t, err)
	if string(data) != "user" {
		t.Fatal("stale observation mutated the target")
	}
}

func TestApplyStateLast(t *testing.T) {
	home := t.TempDir()
	ids := make([]string, 20)
	for i := range ids {
		ids[i] = "skill" + string(rune('a'+i))
	}
	plan := candidate(t, home, "one", ids...)
	mustMkdir(t, plan.RootPath())
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	operations, err := operationsFor(plan, observation)
	if err != nil {
		t.Fatal(err)
	}
	if operations[len(operations)-1].Create == nil || operations[len(operations)-1].Create.Path != ".cortex/install-state.json" {
		t.Fatal("state was not the final operation")
	}
}

func apply(t *testing.T, plan installplan.Plan) Result {
	t.Helper()
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(plan, observation, t.TempDir(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func hasAction(result Result, id, action string) bool {
	for _, item := range result.Actions() {
		if item.LogicalID == id && string(item.Action) == action {
			return true
		}
	}
	return false
}
func assertFile(t *testing.T, file installplan.File, exists bool) {
	t.Helper()
	info, err := os.Stat(file.AbsolutePath())
	if exists {
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != file.DesiredMode().Perm() {
			t.Fatalf("file %s = (%v, %v)", file.LogicalID(), info, err)
		}
		return
	}
	if !os.IsNotExist(err) {
		t.Fatalf("removed file %s exists: %v", file.LogicalID(), err)
	}
}
func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func groupCandidates(t *testing.T, home, version string) []installplan.Plan {
	plans := absentGroupCandidates(t, home, version)
	for _, plan := range plans {
		mustMkdir(t, plan.RootPath())
	}
	return plans
}
func absentGroupCandidates(t *testing.T, home, version string) []installplan.Plan {
	return []installplan.Plan{candidateRuntime(t, home, runtimematrix.RuntimePi, version, "alpha", "beta"), candidateRuntime(t, home, runtimematrix.RuntimeOpenCode, version, "alpha", "beta"), candidateRuntime(t, home, runtimematrix.RuntimeClaudeCode, version, "alpha", "beta")}
}
func groupRequests(t *testing.T, plans ...installplan.Plan) []GroupRequest {
	requests := make([]GroupRequest, len(plans))
	for index, plan := range plans {
		observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
		must(t, err)
		requests[index] = GroupRequest{Plan: plan, Observation: observation}
	}
	return requests
}
func groupOperations(t *testing.T, root string, plans []installplan.Plan) []filetxn.Operation {
	t.Helper()
	operations := make([]filetxn.Operation, 0, len(plans)*len(plans[0].Files()))
	for _, plan := range plans {
		prefix, err := filepath.Rel(root, plan.RootPath())
		must(t, err)
		for _, file := range plan.Files()[:len(plan.Files())-1] {
			operations = append(operations, filetxn.Operation{Create: &filetxn.Create{Path: filepath.ToSlash(filepath.Join(prefix, file.RelativePath())), Data: file.Content(), Mode: file.DesiredMode()}})
		}
	}
	for _, plan := range plans {
		file := plan.Files()[len(plan.Files())-1]
		prefix, err := filepath.Rel(root, plan.RootPath())
		must(t, err)
		operations = append(operations, filetxn.Operation{Create: &filetxn.Create{Path: filepath.ToSlash(filepath.Join(prefix, file.RelativePath())), Data: file.Content(), Mode: file.DesiredMode()}})
	}
	return operations
}
func applyGroup(t *testing.T, plans ...installplan.Plan) GroupResult {
	result, err := applyGroupRequest(t, plans...)
	must(t, err)
	return result
}
func applyGroupRequest(t *testing.T, plans ...installplan.Plan) (GroupResult, error) {
	return ApplyGroup(groupRequests(t, plans...), t.TempDir(), "snapshot")
}

func candidate(t *testing.T, home, version string, ids ...string) installplan.Plan {
	return candidateRuntime(t, home, runtimematrix.RuntimePi, version, ids...)
}
func candidateRuntime(t *testing.T, home string, runtime runtimematrix.RuntimeID, version string, ids ...string) installplan.Plan {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() {
		capabilities := []string{}
		if family == "reasoning" {
			for _, id := range ids {
				capabilities = append(capabilities, "manifests/"+id+".json")
			}
		}
		families[family] = "families/" + family + ".json"
		writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/" + family + ".md", "capabilities": capabilities, "agents": []string{}})
		write(t, root, "routers/"+family+".md", family)
	}
	for _, id := range ids {
		writeJSON(t, root, "manifests/"+id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: id, Family: "reasoning", Source: "sources/" + id + ".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true})
		write(t, root, "sources/"+id+".md", id+version)
	}
	writeJSON(t, root, "catalog.json", map[string]any{"schemaVersion": 1, "families": families})
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	must(t, err)
	sources, err := skillrender.Render(snapshot)
	must(t, err)
	projected, err := skillprojection.Build(runtime, sources)
	must(t, err)
	observations := []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Compatibility: runtimematrix.CompatibilityUnknown}, {ID: runtimematrix.RuntimeOpenCode, Compatibility: runtimematrix.CompatibilityUnknown}, {ID: runtimematrix.RuntimeClaudeCode, Compatibility: runtimematrix.CompatibilityUnknown}}
	for index := range observations {
		if observations[index].ID == runtime {
			observations[index] = runtimematrix.Observation{ID: runtime, Present: true, Version: "1", Compatibility: runtimematrix.Compatible}
		}
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
	must(t, err)
	final, err := projection.BuildPlan(base, []projection.Assessment{projected.Assessment()})
	must(t, err)
	binding, err := skillartifact.Build(projected, final)
	must(t, err)
	bundle, found := binding.Bundle()
	if !found {
		t.Fatal("missing bundle")
	}
	symbolic, err := skilldest.Build(binding)
	must(t, err)
	resolved, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: home})
	must(t, err)
	plan, err := installplan.BuildWithBundle(resolved, bundle)
	must(t, err)
	return plan
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func writeJSON(t *testing.T, root, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	must(t, err)
	write(t, root, path, string(data))
}
func write(t *testing.T, root, path, value string) {
	t.Helper()
	target := filepath.Join(root, path)
	must(t, os.MkdirAll(filepath.Dir(target), 0o700))
	must(t, os.WriteFile(target, []byte(value), 0o600))
}
