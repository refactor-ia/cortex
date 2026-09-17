package installcoord_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// actorAwarePiCandidate builds the canonical detached v2 Pi candidate from the
// repository's real catalog; it mutates no filesystem state outside TempDir roots.
func actorAwarePiCandidate(t *testing.T) installplan.Plan {
	t.Helper()
	snapshot := value(catalog.BuildCatalogSnapshot(filepath.Join("..", "..", "catalog"), "catalog.json", catalog.AdmissionPolicy{}))
	sources := value(skillrender.Render(snapshot))
	assessments := make([]projection.Assessment, 0, 3)
	observations := make([]runtimematrix.Observation, 0, 3)
	for _, id := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		projected := value(skillprojection.Build(id, sources))
		assessments = append(assessments, projected.Assessment())
		observations = append(observations, runtimematrix.Observation{ID: id, Present: true, Version: "fixture", Compatibility: runtimematrix.Compatible})
	}
	final := value(projection.BuildPlan(value(adapterplan.Build(snapshot.Fingerprint(), observations)), assessments))
	binding := value(skillartifact.Build(value(skillprojection.Build(runtimematrix.RuntimePi, sources)), final))
	resolved := value(skillroot.Resolve(value(skilldest.Build(binding)), skillroot.Inputs{Home: t.TempDir()}))
	skills := value(installplan.BuildWithBundle(resolved, mustBundle(t, binding)))
	actors := value(qaactor.Bind(value(qaactor.ProjectPi(value(qaactor.Render(value(qaactor.Sources(snapshot))))))))
	plan := value(installplan.BuildActorAware(skills, actors, "000102030405060708090a0b0c0d0e0f"))
	must(t, os.MkdirAll(plan.RootPath(), 0o700))
	return plan
}

func actorAwareObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "fixture", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
	}
}

func TestPreflightClassifiesActorAwarePiCandidate(t *testing.T) {
	plan := actorAwarePiCandidate(t)
	observation := value(installobserve.Observe(plan, installobserve.DefaultOptions()))
	observations := actorAwareObservations()
	report, err := installcoord.Preflight(observations, []installcoord.Unit{{Plan: plan, Observation: observation}})
	if err != nil || !report.Ready() {
		t.Fatalf("Preflight() = (%#v, %v)", report, err)
	}
	status := report.Statuses()[0]
	if len(status.Actions()) != len(plan.Files()) || status.Evidence().Create != len(plan.Files()) {
		t.Fatalf("actor-aware actions = %#v, want %d creates", status.Actions(), len(plan.Files()))
	}
	for _, file := range plan.Files() {
		if file.Role() == "actor" {
			must(t, os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700))
			must(t, os.WriteFile(file.AbsolutePath(), []byte("user-owned"), 0o600))
			break
		}
	}
	drifted := value(installobserve.Observe(plan, installobserve.DefaultOptions()))
	driftedReport, err := installcoord.Preflight(observations, []installcoord.Unit{{Plan: plan, Observation: drifted}})
	if err != nil || driftedReport.Ready() || driftedReport.Statuses()[0].Ready() {
		t.Fatalf("Preflight() = (%#v, %v), want actor drift conflict", driftedReport, err)
	}
	other := actorAwarePiCandidate(t)
	mismatched, err := installcoord.Preflight(observations, []installcoord.Unit{{Plan: other, Observation: drifted}})
	if !errors.Is(err, installcoord.ErrInvalid) || mismatched.Ready() {
		t.Fatalf("Preflight() = (%#v, %v), want ErrInvalid for a mismatched actor-aware unit", mismatched, err)
	}
}

func value[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestPreflightLocalUpdateNarrowsToTheSelectedRuntime pins the single-target
// fence: the default preflight expects a unit for every admitted runtime, while
// the local update admits only its selected target.
func TestPreflightLocalUpdateNarrowsToTheSelectedRuntime(t *testing.T) {
	plan := actorAwarePiCandidate(t)
	observation := value(installobserve.Observe(plan, installobserve.DefaultOptions()))
	observations := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "9.9.9", Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "9.9.9", Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
	}
	if report, err := installcoord.Preflight(observations, []installcoord.Unit{{Plan: plan, Observation: observation}}); !errors.Is(err, installcoord.ErrInvalid) || report.Ready() {
		t.Fatalf("default Preflight() = (%#v, %v), want an admitted runtime without a unit refused", report, err)
	}
	report, err := installcoord.PreflightLocalUpdate(observations, runtimematrix.RuntimePi, []installcoord.Unit{{Plan: plan, Observation: observation}})
	if err != nil || !report.Ready() || report.Statuses()[0].Outcome != runtimematrix.OutcomePresentUncertified {
		t.Fatalf("local Preflight() = (%#v, %v)", report, err)
	}
}

func TestPreflightAllCleanIsCanonicalAndReadOnly(t *testing.T) {
	observations, plans := compatible(), candidates(t, "one")
	units := observationsFor(t, plans)
	before := tree(t, plans)

	report, err := installcoord.Preflight(observations, units)
	if err != nil || !report.Ready() || !reflect.DeepEqual(tree(t, plans), before) {
		t.Fatalf("Preflight() = (%#v, %v), roots changed = %t", report, err, !reflect.DeepEqual(tree(t, plans), before))
	}
	statuses := report.Statuses()
	if len(statuses) != 3 || statuses[0].RuntimeID != runtimematrix.RuntimePi || statuses[1].RuntimeID != runtimematrix.RuntimeOpenCode || statuses[2].RuntimeID != runtimematrix.RuntimeClaudeCode {
		t.Fatalf("Statuses() = %#v", statuses)
	}
	for _, status := range statuses {
		if status.Outcome != runtimematrix.OutcomePresentCompatible || status.Action != runtimematrix.Configure || !reflect.DeepEqual(status.Actions(), []installcoord.Action{installcoord.ActionCreate, installcoord.ActionCreate}) || status.Evidence().Create != 2 {
			t.Fatalf("unexpected clean status: %#v", status)
		}
	}
	statuses[0].Actions()[0] = installcoord.ActionConflict
	if report.Statuses()[0].Actions()[0] != installcoord.ActionCreate {
		t.Fatal("report exposed mutable actions")
	}
}

func TestPreflightReportsSkipsAndZeroCompatibleDenial(t *testing.T) {
	plans := candidates(t, "one")
	mixed := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "1", Compatibility: runtimematrix.Incompatible},
	}
	report, err := installcoord.Preflight(mixed, observationsFor(t, plans)[:1])
	if err != nil || !report.Ready() {
		t.Fatalf("Preflight() = (%#v, %v)", report, err)
	}
	got := report.Statuses()
	for i, want := range []runtimematrix.Outcome{runtimematrix.OutcomePresentCompatible, runtimematrix.OutcomeKnownIncompatible, runtimematrix.OutcomeUnknownVersion} {
		if got[i].Outcome != want || (i > 0 && len(got[i].Actions()) != 0) {
			t.Fatalf("mixed status = %#v", got[i])
		}
	}
	zero := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "1", Compatibility: runtimematrix.Incompatible},
	}
	report, err = installcoord.Preflight(zero, nil)
	if err != nil || report.Ready() || len(report.Statuses()) != 3 {
		t.Fatalf("zero-compatible Preflight() = (%#v, %v)", report, err)
	}
}

func TestPreflightConflictDeniesEveryParticipant(t *testing.T) {
	plans := candidates(t, "one")
	file := plans[1].Files()[0]
	if err := os.MkdirAll(filepath.Dir(file.AbsolutePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.AbsolutePath(), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := installcoord.Preflight(compatible(), observationsFor(t, plans))
	if err != nil || report.Ready() || report.Statuses()[0].Ready() || report.Statuses()[2].Ready() || report.Statuses()[1].Evidence().Conflict != 1 {
		t.Fatalf("Preflight() = (%#v, %v)", report, err)
	}
}

func TestPreflightRejectsInvalidUnitSets(t *testing.T) {
	plans, otherPlans := candidates(t, "one"), candidates(t, "two")
	units, other := observationsFor(t, plans), observationsFor(t, otherPlans)
	for _, tc := range []struct {
		name  string
		units []installcoord.Unit
	}{
		{"missing", units[:2]},
		{"duplicate", append(append([]installcoord.Unit{}, units...), units[0])},
		{"extra", append(append([]installcoord.Unit{}, units...), installcoord.Unit{})},
		{"mismatched observation", []installcoord.Unit{{Plan: units[0].Plan, Observation: units[1].Observation}, units[1], units[2]}},
		{"mixed snapshot", []installcoord.Unit{{Plan: other[0].Plan, Observation: other[0].Observation}, units[1], units[2]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, err := installcoord.Preflight(compatible(), tc.units)
			if !errors.Is(err, installcoord.ErrInvalid) || report.Ready() || len(report.Statuses()) != 0 {
				t.Fatalf("Preflight() = (%#v, %v)", report, err)
			}
		})
	}
}

func compatible() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
	}
}

func observationsFor(t *testing.T, plans []installplan.Plan) []installcoord.Unit {
	t.Helper()
	units := make([]installcoord.Unit, len(plans))
	for i, plan := range plans {
		observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
		must(t, err)
		units[i] = installcoord.Unit{Plan: plan, Observation: observation}
	}
	return units
}

func candidates(t *testing.T, version string) []installplan.Plan {
	t.Helper()
	root, families := t.TempDir(), map[string]string{}
	for _, id := range catalog.ApprovedFamilyIDs() {
		families[id] = "families/" + id + ".json"
		capabilities := "[]"
		if id == "reasoning" {
			capabilities = `["manifests/alpha.json"]`
		}
		write(t, root, families[id], `{"schemaVersion":1,"id":"`+id+`","router":"routers/`+id+`.md","capabilities":`+capabilities+`,"agents":[]}`)
		write(t, root, "routers/"+id+".md", id)
	}
	write(t, root, "manifests/alpha.json", `{"schemaVersion":1,"id":"alpha","description":"alpha","family":"reasoning","source":"sources/alpha.md","activation":"automatic","provenance":"cortex-owned","license":"CC-BY-SA-4.0","redistributionAllowed":true}`)
	write(t, root, "sources/alpha.md", version)
	write(t, root, "catalog.json", `{"schemaVersion":1,"families":`+jsonFamilies(families)+`}`)
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	must(t, err)
	sources, err := skillrender.Render(snapshot)
	must(t, err)
	base, err := adapterplan.Build(snapshot.Fingerprint(), compatible())
	must(t, err)
	ids := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	projected := make([]skillprojection.Plan, len(ids))
	assessments := make([]projection.Assessment, len(ids))
	for i, id := range ids {
		projected[i], err = skillprojection.Build(id, sources)
		must(t, err)
		assessments[i] = projected[i].Assessment()
	}
	final, err := projection.BuildPlan(base, assessments)
	must(t, err)
	plans, home := make([]installplan.Plan, len(ids)), t.TempDir()
	for i := range ids {
		binding, err := skillartifact.Build(projected[i], final)
		must(t, err)
		symbolic, err := skilldest.Build(binding)
		must(t, err)
		resolved, err := skillroot.Resolve(symbolic, skillroot.Inputs{Home: home})
		must(t, err)
		plans[i], err = installplan.BuildWithBundle(resolved, mustBundle(t, binding))
		must(t, err)
		must(t, os.MkdirAll(plans[i].RootPath(), 0o700))
	}
	return plans
}

func mustBundle(t *testing.T, binding skillartifact.Binding) artifact.Bundle {
	t.Helper()
	bundle, ok := binding.Bundle()
	if !ok {
		t.Fatal("missing bundle")
	}
	return bundle
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func write(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, name)
	must(t, os.MkdirAll(filepath.Dir(path), 0o700))
	must(t, os.WriteFile(path, []byte(data), 0o600))
}
func jsonFamilies(families map[string]string) string {
	data, _ := json.Marshal(families)
	return string(data)
}
func tree(t *testing.T, plans []installplan.Plan) [][]string {
	t.Helper()
	out := make([][]string, len(plans))
	for i, plan := range plans {
		entries, err := os.ReadDir(plan.RootPath())
		must(t, err)
		for _, entry := range entries {
			out[i] = append(out[i], entry.Name())
		}
	}
	return out
}
