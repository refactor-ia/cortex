package installtxn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qaactor"
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
	plans := []installplan.Plan{candidateRuntime(t, home, runtimematrix.RuntimePi, version, "alpha", "beta"), candidateRuntime(t, home, runtimematrix.RuntimeOpenCode, version, "alpha", "beta"), candidateRuntime(t, home, runtimematrix.RuntimeClaudeCode, version, "alpha", "beta")}
	for _, plan := range plans {
		mustMkdir(t, plan.RootPath())
	}
	return plans
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

func TestTransactionIDParse(t *testing.T) {
	const valid = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{"canonical", valid, true},
		{"zero", "0000000000000000000000000000000000000000000000000000000000000000", false},
		{"uppercase", "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef", false},
		{"short", valid[:63], false},
		{"non-hex", valid[:63] + "g", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, err := ParseTransactionID(tc.raw)
			want := ""
			if tc.valid {
				want = tc.raw
			}
			if (err == nil) != tc.valid || id.Valid() != tc.valid || id.String() != want {
				t.Fatalf("ParseTransactionID(%q) = (%q, %v), valid = %t", tc.raw, id.String(), err, id.Valid())
			}
		})
	}
}

func TestTransactionIDBindsCandidateAndSnapshot(t *testing.T) {
	home := physicalTempDir(t)
	candidate := actorAwareCandidateWith(t, home, "000102030405060708090a0b0c0d0e0f")
	mustMkdir(t, candidate.RootPath())
	snapshot := filetxn.Snapshot{Manifest: filetxn.Manifest{Version: 2, Entries: []filetxn.Entry{{Path: "agents/cortex-test.md", Exists: true, Mode: 0o600, SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}}}
	baseline, err := transactionID(candidate, snapshot)
	must(t, err)
	if same, sameErr := transactionID(candidate, snapshot); sameErr != nil || same != baseline {
		t.Fatalf("same transaction ID = (%q, %v), want %q", same.String(), sameErr, baseline.String())
	}
	changedManifest := snapshot
	changedManifest.Manifest.Entries = append([]filetxn.Entry(nil), snapshot.Manifest.Entries...)
	changedManifest.Manifest.Entries[0].Mode = 0o644
	for _, tc := range []struct {
		name      string
		candidate installplan.Plan
		snapshot  filetxn.Snapshot
	}{
		{"root", func() installplan.Plan {
			candidate := actorAwareCandidate(t)
			mustMkdir(t, candidate.RootPath())
			return candidate
		}(), snapshot},
		{"candidate state", actorAwareCandidateWith(t, home, "111102030405060708090a0b0c0d0e0f"), snapshot},
		{"before manifest", candidate, changedManifest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, computeErr := transactionID(tc.candidate, tc.snapshot)
			if computeErr == nil && got == baseline {
				t.Fatalf("transaction ID did not bind %s", tc.name)
			}
		})
	}
}

func TestResultDoesNotEncodePathsOrContent(t *testing.T) {
	result := Result{actions: []Action{{LogicalID: "skill/example", Action: ownership.Create}}, transactionID: TransactionID{value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}
	for index := 0; index < reflect.TypeOf(result).NumField(); index++ {
		if reflect.TypeOf(result).Field(index).PkgPath == "" {
			t.Fatalf("Result exposes field %q", reflect.TypeOf(result).Field(index).Name)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("json.Marshal(Result) = (%s, %v)", encoded, err)
	}
}

func TestTransactionIDRejectsSymlinkedCandidateRoot(t *testing.T) {
	realHome, linkParent := physicalTempDir(t), physicalTempDir(t)
	linkedHome := filepath.Join(linkParent, "home")
	must(t, os.Symlink(realHome, linkedHome))
	candidate := actorAwareCandidateWith(t, linkedHome, "000102030405060708090a0b0c0d0e0f")
	mustMkdir(t, candidate.RootPath())

	_, err := transactionID(candidate, filetxn.Snapshot{Manifest: filetxn.Manifest{Version: 2}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("transactionID() error = %v", err)
	}
}

func TestApplyVerifiedMaterializesActorAwareCandidateStateLast(t *testing.T) {
	candidate := actorAwareCandidate(t)
	mustMkdir(t, candidate.RootPath())
	cwd := physicalTempDir(t)
	var trace []string

	result, err := applyVerifiedWith(candidate, cwd, t.TempDir(), "snapshot", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
		for _, operation := range operations {
			switch {
			case operation.Create != nil:
				trace = append(trace, operation.Create.Path)
			case operation.Replace != nil:
				trace = append(trace, operation.Replace.Path)
			case operation.Remove != nil:
				trace = append(trace, operation.Remove.Path)
			}
		}
		return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backupRoot, backupName, directories, operations, verify, func(snapshot filetxn.Snapshot) error {
			trace = append(trace, "finalize")
			return finalize(snapshot)
		})
	})
	if err != nil || len(result.Actions()) != len(candidate.Files()) || !result.TransactionID().Valid() {
		t.Fatalf("ApplyVerified() = (%#v, %v)", result, err)
	}
	for _, file := range candidate.Files() {
		data, readErr := os.ReadFile(file.AbsolutePath())
		if readErr != nil || !bytes.Equal(data, file.Content()) || mode(t, file.AbsolutePath()) != file.DesiredMode() {
			t.Fatalf("final file %q = (%q, %#o, %v)", file.LogicalID(), data, mode(t, file.AbsolutePath()), readErr)
		}
	}
	expected := make([]string, 0, len(candidate.Files()))
	for _, role := range []string{"skill", "actor"} {
		for _, file := range candidate.Files()[:len(candidate.Files())-1] {
			if file.Role() == role {
				expected = append(expected, file.RelativePath())
			}
		}
	}
	expected = append(expected, ".cortex/install-state.json", "finalize")
	if !reflect.DeepEqual(trace, expected) {
		t.Fatalf("transaction trace = %v, want %v", trace, expected)
	}
	observation, observeErr := installobserve.Observe(candidate, installobserve.DefaultOptions())
	must(t, observeErr)
	classified, classifyErr := installobserve.ClassifyFilesystem(candidate, observation)
	must(t, classifyErr)
	if classified.StateAction() != ownership.Unchanged {
		t.Fatalf("state action = %q", classified.StateAction())
	}
	for _, decision := range classified.ArtifactDecisions() {
		if decision.ObservedOwnership != ownership.CortexOwned || decision.Action != ownership.Unchanged {
			t.Fatalf("final decision = %#v", decision)
		}
	}
	shadows, shadowErr := installobserve.ObserveActorShadows(candidate, observation, cwd)
	if shadowErr != nil || !shadows.Clean() {
		t.Fatalf("final shadows = (%#v, %v)", shadows, shadowErr)
	}
	noOp, noOpErr := ApplyVerified(candidate, cwd, t.TempDir(), "snapshot-noop")
	if noOpErr != nil || noOp.TransactionID().Valid() {
		t.Fatalf("no-op ApplyVerified() = (%#v, %v)", noOp, noOpErr)
	}
}

func TestApplyVerifiedRestoresV1OriginOnFinalVerificationFailure(t *testing.T) {
	candidate := actorAwareCandidate(t)
	mustMkdir(t, candidate.RootPath())
	cwd := physicalTempDir(t)
	oldState, oldSkills := writeV1Origin(t, candidate)

	_, err := applyVerifiedWith(candidate, cwd, t.TempDir(), "snapshot", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
		return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backupRoot, backupName, directories, operations, func() error {
			for _, file := range candidate.Files() {
				data, readErr := os.ReadFile(file.AbsolutePath())
				if readErr != nil || !bytes.Equal(data, file.Content()) || mode(t, file.AbsolutePath()) != file.DesiredMode() {
					t.Fatalf("candidate file %q = (%q, %#o, %v)", file.LogicalID(), data, mode(t, file.AbsolutePath()), readErr)
				}
			}
			if verifyErr := verify(); verifyErr != nil {
				return verifyErr
			}
			return errors.New("injected final verification failure")
		}, finalize)
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}

	state := candidate.Files()[len(candidate.Files())-1]
	assertExactBytesAndMode(t, state.AbsolutePath(), oldState, state.DesiredMode())
	for _, file := range candidate.Files() {
		switch file.Role() {
		case "skill":
			assertExactBytesAndMode(t, file.AbsolutePath(), oldSkills[file.LogicalID()], file.DesiredMode())
		case "actor":
			if _, statErr := os.Lstat(file.AbsolutePath()); !os.IsNotExist(statErr) {
				t.Fatalf("actor %q remains after rollback: %v", file.LogicalID(), statErr)
			}
		}
	}
}

func TestApplyVerifiedRejectsShadowBeforeMutation(t *testing.T) {
	candidate := actorAwareCandidate(t)
	mustMkdir(t, candidate.RootPath())
	cwd := physicalTempDir(t)
	var actor installplan.File
	for _, file := range candidate.Files() {
		if file.Role() == "actor" {
			actor = file
			break
		}
	}
	mustMkdir(t, filepath.Join(cwd, ".pi", "subagents"))
	must(t, os.WriteFile(filepath.Join(cwd, ".pi", "subagents", "shadow.md"), actor.Content(), actor.DesiredMode()))
	backups := t.TempDir()

	_, err := ApplyVerified(candidate, cwd, backups, "snapshot")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	for _, file := range candidate.Files() {
		assertFile(t, file, false)
	}
	if _, statErr := os.Lstat(filepath.Join(backups, "snapshot")); !os.IsNotExist(statErr) {
		t.Fatalf("backup was created: %v", statErr)
	}
}

func TestApplyVerifiedRollsBackFinalReadbackFailure(t *testing.T) {
	candidate := actorAwareCandidate(t)
	mustMkdir(t, candidate.RootPath())
	cwd := physicalTempDir(t)
	state := candidate.Files()[len(candidate.Files())-1]

	_, err := applyVerifiedWith(candidate, cwd, t.TempDir(), "snapshot", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
		return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backupRoot, backupName, directories, operations, func() error {
			must(t, os.WriteFile(state.AbsolutePath(), []byte("drift"), state.DesiredMode()))
			return verify()
		}, finalize)
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	for _, file := range candidate.Files()[:len(candidate.Files())-1] {
		assertFile(t, file, false)
	}
	data, readErr := os.ReadFile(state.AbsolutePath())
	if readErr != nil || bytes.Equal(data, state.Content()) {
		t.Fatalf("state drift = (%q, %v)", data, readErr)
	}
}

func actorAwareCandidate(t *testing.T) installplan.Plan {
	return actorAwareCandidateWith(t, physicalTempDir(t), "000102030405060708090a0b0c0d0e0f")
}

func actorAwareCandidateWith(t *testing.T, home, installationID string) installplan.Plan {
	t.Helper()
	root := filepath.Join("..", "..", "catalog")
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	must(t, err)
	sources, err := skillrender.Render(snapshot)
	must(t, err)
	projected, err := skillprojection.Build(runtimematrix.RuntimePi, sources)
	must(t, err)
	assessments := make([]projection.Assessment, 0, 3)
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, runtimeID := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		assessment, buildErr := skillprojection.Build(runtimeID, sources)
		must(t, buildErr)
		assessments = append(assessments, assessment.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: runtimeID, Present: true, Version: "test", Compatibility: runtimematrix.Compatible})
	}
	base, err := adapterplan.Build(snapshot.Fingerprint(), observations)
	must(t, err)
	plan, err := projection.BuildPlan(base, assessments)
	must(t, err)
	binding, err := skillartifact.Build(projected, plan)
	must(t, err)
	bundle, found := binding.Bundle()
	if !found {
		t.Fatal("missing skill bundle")
	}
	destinations, err := skilldest.Build(binding)
	must(t, err)
	resolved, err := skillroot.Resolve(destinations, skillroot.Inputs{Home: home})
	must(t, err)
	skills, err := installplan.BuildWithBundle(resolved, bundle)
	must(t, err)
	actorSources, err := qaactor.Sources(snapshot)
	must(t, err)
	set, err := qaactor.Render(actorSources)
	must(t, err)
	actors, err := qaactor.ProjectPi(set)
	must(t, err)
	actorBinding, err := qaactor.Bind(actors)
	must(t, err)
	candidate, err := installplan.BuildActorAware(skills, actorBinding, installstate.InstallationID(installationID))
	must(t, err)
	return candidate
}

func writeV1Origin(t *testing.T, candidate installplan.Plan) ([]byte, map[string][]byte) {
	t.Helper()
	inputs := make([]installstate.ArtifactInput, 0)
	skills := make(map[string][]byte)
	for _, file := range candidate.Files() {
		if file.Role() != "skill" {
			continue
		}
		data := []byte("v1-origin:" + file.LogicalID())
		digest := sha256.Sum256(data)
		inputs = append(inputs, installstate.ArtifactInput{
			LogicalID: file.LogicalID(), RelativePath: file.RelativePath(), SHA256: hex.EncodeToString(digest[:]),
		})
		skills[file.LogicalID()] = data
		mustMkdir(t, filepath.Dir(file.AbsolutePath()))
		must(t, os.WriteFile(file.AbsolutePath(), data, file.DesiredMode()))
	}
	manifest, err := installstate.New(candidate.RuntimeID(), candidate.RootKind(), candidate.SnapshotFingerprint(), inputs)
	must(t, err)
	state, err := installstate.Encode(manifest)
	must(t, err)
	stateFile := candidate.Files()[len(candidate.Files())-1]
	mustMkdir(t, filepath.Dir(stateFile.AbsolutePath()))
	must(t, os.WriteFile(stateFile.AbsolutePath(), state, stateFile.DesiredMode()))
	return state, skills
}

func assertExactBytesAndMode(t *testing.T, path string, want []byte, wantMode fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, want) || wantMode != mode(t, path) {
		t.Fatalf("file %q = (%q, %#o, %v)", path, data, mode(t, path), err)
	}
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	return root
}

func TestApplyVerifiedRejectsPreTransactionAssetRace(t *testing.T) {
	candidate := actorAwareCandidate(t)
	mustMkdir(t, candidate.RootPath())
	cwd := physicalTempDir(t)
	asset := candidate.Files()[0]

	_, err := applyVerifiedWith(candidate, cwd, t.TempDir(), "snapshot", func(root, backupRoot, backupName string, directories []filetxn.Directory, operations []filetxn.Operation, verify func() error, finalize func(filetxn.Snapshot) error) (filetxn.Snapshot, error) {
		mustMkdir(t, filepath.Dir(asset.AbsolutePath()))
		must(t, os.WriteFile(asset.AbsolutePath(), []byte("raced"), asset.DesiredMode()))
		return filetxn.ApplyOperationsWithDirectoriesAndFinalize(root, backupRoot, backupName, directories, operations, verify, finalize)
	})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("ApplyVerified() error = %v", err)
	}
	data, readErr := os.ReadFile(asset.AbsolutePath())
	if readErr != nil || string(data) != "raced" {
		t.Fatalf("raced asset = (%q, %v)", data, readErr)
	}
	for _, file := range candidate.Files()[1:] {
		assertFile(t, file, false)
	}
}
