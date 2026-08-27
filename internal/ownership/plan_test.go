package ownership_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

const testFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func hash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func bundle(t *testing.T, ids ...string) artifact.Bundle {
	t.Helper()
	contents, hashes := make(map[string][]byte, len(ids)), make(map[string]string, len(ids))
	payloads := make([]artifact.PayloadInput, 0, len(ids))
	for _, id := range ids {
		content := []byte("content:" + id)
		contents[id], hashes[id] = content, hash(string(content))
		payloads = append(payloads, artifact.PayloadInput{LogicalID: id, Content: content})
	}
	encoded, err := json.Marshal(hashes)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := artifact.DecodeManifest([]byte(`{"schemaVersion":1,"owner":"cortex","snapshotFingerprint":"` + testFingerprint + `","runtime":"pi","projectionResult":"exact","artifacts":` + string(encoded) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	base, err := adapterplan.Build(testFingerprint, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Compatibility: runtimematrix.CompatibilityUnknown},
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := projection.NewAssessment(runtimematrix.RuntimePi, testFingerprint, projection.Exact, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := projection.BuildPlan(base, []projection.Assessment{assessment})
	if err != nil {
		t.Fatal(err)
	}
	got, err := artifact.Bind(manifest, plan, payloads)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func desiredHash(t *testing.T, bundle artifact.Bundle, id string) string {
	t.Helper()
	for _, artifact := range bundle.Artifacts() {
		if artifact.LogicalID() == id {
			return artifact.SHA256()
		}
	}
	t.Fatalf("missing desired artifact %q", id)
	return ""
}

func TestBuildMatrixAndConflictSafety(t *testing.T) {
	bundle := bundle(t, "desired/create", "desired/same", "desired/replace", "desired/conflict", "desired/unrelated-conflict")
	same := desiredHash(t, bundle, "desired/same")
	plan, err := ownership.Build(bundle, []ownership.ObservedArtifact{
		{LogicalID: "desired/same", CurrentHash: same, Ownership: ownership.CortexOwned},
		{LogicalID: "desired/replace", CurrentHash: strings.Repeat("b", 64), Ownership: ownership.CortexOwned},
		{LogicalID: "desired/conflict", CurrentHash: strings.Repeat("c", 64), Ownership: ownership.UserOwned},
		{LogicalID: "desired/unrelated-conflict", CurrentHash: strings.Repeat("d", 64), Ownership: ownership.Unrelated},
		{LogicalID: "stale/remove", CurrentHash: strings.Repeat("e", 64), Ownership: ownership.CortexOwned},
		{LogicalID: "extra/preserve-user", CurrentHash: strings.Repeat("e", 64), Ownership: ownership.UserOwned},
		{LogicalID: "extra/preserve-unrelated", CurrentHash: strings.Repeat("f", 64), Ownership: ownership.Unrelated},
	})
	if err != nil || plan.Ready() || plan.HasChanges() {
		t.Fatalf("Build() = (%#v, %v)", plan, err)
	}
	want := []ownership.Decision{
		{LogicalID: "desired/conflict", ObservedOwnership: ownership.UserOwned, DesiredHash: desiredHash(t, bundle, "desired/conflict"), CurrentHash: strings.Repeat("c", 64), Action: ownership.Conflict},
		{LogicalID: "desired/create", ObservedOwnership: ownership.CortexOwned, DesiredHash: desiredHash(t, bundle, "desired/create"), Action: ownership.Create},
		{LogicalID: "desired/replace", ObservedOwnership: ownership.CortexOwned, DesiredHash: desiredHash(t, bundle, "desired/replace"), CurrentHash: strings.Repeat("b", 64), Action: ownership.Replace},
		{LogicalID: "desired/same", ObservedOwnership: ownership.CortexOwned, DesiredHash: same, CurrentHash: same, Action: ownership.Unchanged},
		{LogicalID: "desired/unrelated-conflict", ObservedOwnership: ownership.Unrelated, DesiredHash: desiredHash(t, bundle, "desired/unrelated-conflict"), CurrentHash: strings.Repeat("d", 64), Action: ownership.Conflict},
		{LogicalID: "extra/preserve-unrelated", ObservedOwnership: ownership.Unrelated, CurrentHash: strings.Repeat("f", 64), Action: ownership.Preserve},
		{LogicalID: "extra/preserve-user", ObservedOwnership: ownership.UserOwned, CurrentHash: strings.Repeat("e", 64), Action: ownership.Preserve},
		{LogicalID: "stale/remove", ObservedOwnership: ownership.CortexOwned, CurrentHash: strings.Repeat("e", 64), Action: ownership.Remove},
	}
	if got := plan.Decisions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Decisions() = %#v, want %#v", got, want)
	}
}

func TestBuildReadyChangesAndObservedOnlyActions(t *testing.T) {
	bundle := bundle(t, "desired/create", "desired/replace", "desired/same")
	same := desiredHash(t, bundle, "desired/same")
	plan, err := ownership.Build(bundle, []ownership.ObservedArtifact{
		{LogicalID: "desired/replace", CurrentHash: strings.Repeat("b", 64), Ownership: ownership.CortexOwned},
		{LogicalID: "desired/same", CurrentHash: same, Ownership: ownership.CortexOwned},
		{LogicalID: "stale/remove", CurrentHash: strings.Repeat("c", 64), Ownership: ownership.CortexOwned},
		{LogicalID: "extra/user", CurrentHash: strings.Repeat("d", 64), Ownership: ownership.UserOwned},
		{LogicalID: "extra/unrelated", CurrentHash: strings.Repeat("e", 64), Ownership: ownership.Unrelated},
	})
	if err != nil || !plan.Ready() || !plan.HasChanges() {
		t.Fatalf("Build() = (%#v, %v)", plan, err)
	}
	for _, decision := range plan.Decisions() {
		wantTouch := decision.Action == ownership.Create || decision.Action == ownership.Replace || decision.Action == ownership.Remove
		if decision.TouchAllowed != wantTouch {
			t.Fatalf("decision %q touch = %t, want %t", decision.LogicalID, decision.TouchAllowed, wantTouch)
		}
	}
}

func TestBuildNoChangesAndEmptyObserved(t *testing.T) {
	bundle := bundle(t, "desired/same")
	same := desiredHash(t, bundle, "desired/same")
	for _, tc := range []struct {
		name     string
		observed []ownership.ObservedArtifact
		changes  bool
	}{
		{"unchanged and preserved", []ownership.ObservedArtifact{{LogicalID: "desired/same", CurrentHash: same, Ownership: ownership.CortexOwned}, {LogicalID: "extra/user", CurrentHash: strings.Repeat("b", 64), Ownership: ownership.UserOwned}, {LogicalID: "extra/unrelated", CurrentHash: strings.Repeat("c", 64), Ownership: ownership.Unrelated}}, false},
		{"empty observed creates desired", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ownership.Build(bundle, tc.observed)
			if err != nil || !plan.Ready() || plan.HasChanges() != tc.changes {
				t.Fatalf("Build() = (%#v, %v)", plan, err)
			}
			if tc.observed == nil && (len(plan.Decisions()) != 1 || plan.Decisions()[0].Action != ownership.Create || !plan.Decisions()[0].TouchAllowed) {
				t.Fatalf("empty observed plan = %#v", plan.Decisions())
			}
		})
	}
}

func TestBuildRejectsInvalidInputsWithoutLeaks(t *testing.T) {
	valid := bundle(t, "desired/valid")
	for _, tc := range []struct {
		name     string
		bundle   artifact.Bundle
		observed []ownership.ObservedArtifact
	}{
		{"zero bundle", artifact.Bundle{}, nil},
		{"duplicate ID", valid, []ownership.ObservedArtifact{{LogicalID: "secret/item", CurrentHash: strings.Repeat("a", 64), Ownership: ownership.CortexOwned}, {LogicalID: "secret/item", CurrentHash: strings.Repeat("b", 64), Ownership: ownership.CortexOwned}}},
		{"invalid ID", valid, []ownership.ObservedArtifact{{LogicalID: "secret_item", CurrentHash: strings.Repeat("a", 64), Ownership: ownership.CortexOwned}}},
		{"invalid hash", valid, []ownership.ObservedArtifact{{LogicalID: "secret/item", CurrentHash: "ABC", Ownership: ownership.CortexOwned}}},
		{"invalid ownership", valid, []ownership.ObservedArtifact{{LogicalID: "secret/item", CurrentHash: strings.Repeat("a", 64), Ownership: "private"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := ownership.Build(tc.bundle, tc.observed)
			if err == nil || plan.Ready() || plan.HasChanges() || len(plan.Decisions()) != 0 || err.Error() != "ownership plan: invalid input" || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "ABC") || strings.Contains(err.Error(), "private") {
				t.Fatalf("Build() = (%#v, %v)", plan, err)
			}
		})
	}
}

func TestBuildIsOrderIndependentAndImmutable(t *testing.T) {
	bundle := bundle(t, "desired/a", "desired/b")
	observed := []ownership.ObservedArtifact{
		{LogicalID: "desired/a", CurrentHash: desiredHash(t, bundle, "desired/a"), Ownership: ownership.CortexOwned},
		{LogicalID: "stale/c", CurrentHash: strings.Repeat("c", 64), Ownership: ownership.CortexOwned},
	}
	original := append([]ownership.ObservedArtifact(nil), observed...)
	first, err := ownership.Build(bundle, observed)
	if err != nil || !reflect.DeepEqual(observed, original) {
		t.Fatalf("Build() = (%#v, %v)", first, err)
	}
	observed[0], observed[1] = observed[1], observed[0]
	second, err := ownership.Build(bundle, observed)
	if err != nil || !reflect.DeepEqual(first.Decisions(), second.Decisions()) || !reflect.DeepEqual(observed, []ownership.ObservedArtifact{original[1], original[0]}) {
		t.Fatalf("Build() = (%#v, %v)", second, err)
	}
	decisions := first.Decisions()
	decisions[0].LogicalID, decisions[0].TouchAllowed = "changed", false
	if first.Decisions()[0].LogicalID == "changed" || !reflect.DeepEqual(first.Decisions(), second.Decisions()) {
		t.Fatal("Build() exposed mutable plan state")
	}
}
