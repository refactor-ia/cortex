package installtxn

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/catalog"
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
	plan := candidate(t, t.TempDir(), "one", "alpha"); mustMkdir(t, filepath.Dir(plan.Files()[0].AbsolutePath())); observation, err := installobserve.Observe(plan, installobserve.DefaultOptions()); must(t, err)
	must(t, os.WriteFile(plan.Files()[0].AbsolutePath(), []byte("user"), 0o600)); _, err = Apply(plan, observation, t.TempDir(), "snapshot")
	if !errors.Is(err, ErrInvalid) { t.Fatalf("Apply() error = %v", err) }; data, err := os.ReadFile(plan.Files()[0].AbsolutePath()); must(t, err); if string(data) != "user" { t.Fatal("stale observation mutated the target") }
}

func TestApplyStateLastAndLateFailureRollback(t *testing.T) {
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

	state := plan.Files()[len(plan.Files())-1].AbsolutePath()
	ready := make(chan error, 1)
	go func(skill string) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Lstat(skill); err == nil {
				ready <- os.Mkdir(state, 0o700)
				return
			}
			time.Sleep(time.Millisecond)
		}
		ready <- errors.New("skill creation was not observed")
	}(plan.Files()[0].AbsolutePath())
	_, err = Apply(plan, observation, t.TempDir(), "snapshot")
	if raceErr := <-ready; raceErr != nil {
		t.Fatal(raceErr)
	}
	if err == nil {
		t.Fatal("Apply() succeeded despite late state conflict")
	}
	for _, file := range plan.Files()[:len(plan.Files())-1] {
		assertFile(t, file, false)
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

func candidate(t *testing.T, home, version string, ids ...string) installplan.Plan {
	t.Helper(); root, families := t.TempDir(), map[string]string{}
	for _, family := range catalog.ApprovedFamilyIDs() { capabilities := []string{}; if family == "reasoning" { for _, id := range ids { capabilities = append(capabilities, "manifests/"+id+".json") } }; families[family] = "families/"+family+".json"; writeJSON(t, root, families[family], map[string]any{"schemaVersion": 1, "id": family, "router": "routers/"+family+".md", "capabilities": capabilities, "agents": []string{}}); write(t, root, "routers/"+family+".md", family) }
	for _, id := range ids { writeJSON(t, root, "manifests/"+id+".json", catalog.CapabilityManifest{SchemaVersion: 1, ID: id, Description: id, Family: "reasoning", Source: "sources/"+id+".md", Activation: catalog.ActivationAutomatic, Provenance: catalog.ProvenanceCortexOwned, License: "CC-BY-SA-4.0", RedistributionAllowed: true}); write(t, root, "sources/"+id+".md", id+version) }
	writeJSON(t, root, "catalog.json", map[string]any{"schemaVersion": 1, "families": families}); snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{}); must(t, err)
	sources, err := skillrender.Render(snapshot); must(t, err); projected, err := skillprojection.Build(runtimematrix.RuntimePi, sources); must(t, err)
	base, err := adapterplan.Build(snapshot.Fingerprint(), []runtimematrix.Observation{{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible}, {ID: runtimematrix.RuntimeOpenCode, Compatibility: runtimematrix.CompatibilityUnknown}, {ID: runtimematrix.RuntimeClaudeCode, Compatibility: runtimematrix.CompatibilityUnknown}}); must(t, err)
	final, err := projection.BuildPlan(base, []projection.Assessment{projected.Assessment()}); must(t, err); binding, err := skillartifact.Build(projected, final); must(t, err); bundle, found := binding.Bundle(); if !found { t.Fatal("missing bundle") }
	symbolic, err := skilldest.Build(binding); must(t, err); resolved, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: home}); must(t, err); plan, err := installplan.BuildWithBundle(resolved, bundle); must(t, err); return plan
}
func must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }
func writeJSON(t *testing.T, root, path string, value any) { t.Helper(); data, err := json.Marshal(value); must(t, err); write(t, root, path, string(data)) }
func write(t *testing.T, root, path, value string) { t.Helper(); target := filepath.Join(root, path); must(t, os.MkdirAll(filepath.Dir(target), 0o700)); must(t, os.WriteFile(target, []byte(value), 0o600)) }
