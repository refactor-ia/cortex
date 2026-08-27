package adapterplan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

const fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBuildMixedOutcomes(t *testing.T) {
	plan, err := Build(fingerprint, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Incompatible},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := Plan{
		SnapshotFingerprint: fingerprint,
		Results: []RuntimeResult{
			{ID: runtimematrix.RuntimePi, Outcome: runtimematrix.OutcomeAbsent, Action: runtimematrix.Warn},
			{ID: runtimematrix.RuntimeOpenCode, Outcome: runtimematrix.OutcomeKnownIncompatible, Action: runtimematrix.Skip},
			{ID: runtimematrix.RuntimeClaudeCode, Outcome: runtimematrix.OutcomePresentCompatible, Action: runtimematrix.Configure, IncludeInTransaction: true, TouchAllowed: true},
		},
		TransactionTargets: []runtimematrix.RuntimeID{runtimematrix.RuntimeClaudeCode},
		AllOrNothing:       true,
	}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("Build() = %#v, want %#v", plan, want)
	}
}

func TestBuildAllCompatibleUsesOneOrderedTransaction(t *testing.T) {
	plan, err := Build(fingerprint, compatibleObservations())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []runtimematrix.RuntimeID{
		runtimematrix.RuntimePi,
		runtimematrix.RuntimeOpenCode,
		runtimematrix.RuntimeClaudeCode,
	}
	if !reflect.DeepEqual(plan.TransactionTargets, want) {
		t.Errorf("TransactionTargets = %#v, want %#v", plan.TransactionTargets, want)
	}
	if !plan.AllOrNothing || plan.ReportOnly {
		t.Errorf("transaction flags = all-or-nothing:%t report-only:%t, want true:false", plan.AllOrNothing, plan.ReportOnly)
	}
}

func TestBuildNoCompatibleIsReportOnly(t *testing.T) {
	plan, err := Build(fingerprint, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Incompatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if plan.TransactionTargets == nil || len(plan.TransactionTargets) != 0 {
		t.Errorf("TransactionTargets = %#v, want non-nil empty slice", plan.TransactionTargets)
	}
	if plan.AllOrNothing || !plan.ReportOnly {
		t.Errorf("report-only flags = all-or-nothing:%t report-only:%t, want false:true", plan.AllOrNothing, plan.ReportOnly)
	}
	outcomes := []runtimematrix.Outcome{
		plan.Results[0].Outcome,
		plan.Results[1].Outcome,
		plan.Results[2].Outcome,
	}
	want := []runtimematrix.Outcome{
		runtimematrix.OutcomeAbsent,
		runtimematrix.OutcomeKnownIncompatible,
		runtimematrix.OutcomeUnknownVersion,
	}
	if !reflect.DeepEqual(outcomes, want) {
		t.Errorf("outcomes = %#v, want absent, incompatible, and unknown version distinct", outcomes)
	}
}

func TestBuildBindsOnlyTheValidFingerprint(t *testing.T) {
	observations := compatibleObservations()
	otherFingerprint := strings.Repeat("b", 64)
	plan, err := Build(fingerprint, observations)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	other, err := Build(otherFingerprint, observations)
	if err != nil {
		t.Fatalf("Build() second error = %v", err)
	}
	if plan.SnapshotFingerprint != fingerprint || other.SnapshotFingerprint != otherFingerprint {
		t.Errorf("fingerprints = %q and %q, want exact valid inputs", plan.SnapshotFingerprint, other.SnapshotFingerprint)
	}
	plan.SnapshotFingerprint = ""
	other.SnapshotFingerprint = ""
	if !reflect.DeepEqual(plan, other) {
		t.Errorf("changing valid fingerprint changed plan content: %#v != %#v", plan, other)
	}
}

func TestBuildRejectsInvalidFingerprintsWithoutLeaks(t *testing.T) {
	invalid := []string{
		"",
		strings.Repeat("a", 63),
		strings.Repeat("a", 65),
		strings.Repeat("A", 64),
		strings.Repeat("g", 64),
		"0x" + strings.Repeat("a", 62),
		strings.Repeat("a", 63) + "\n",
		strings.Repeat("a", 63) + " ",
	}
	for _, value := range invalid {
		t.Run("invalid fingerprint", func(t *testing.T) {
			plan, err := Build(value, compatibleObservations())
			if err == nil {
				t.Fatal("Build() error = nil, want error")
			}
			if !reflect.DeepEqual(plan, Plan{}) {
				t.Errorf("Build() plan = %#v, want zero Plan", plan)
			}
			for _, forbidden := range []string{value, "1.0.0", "2.0.0", "3.0.0"} {
				if forbidden != "" && strings.Contains(err.Error(), forbidden) {
					t.Errorf("error %q leaked %q", err, forbidden)
				}
			}
		})
	}
}

func TestBuildRejectsInvalidObservations(t *testing.T) {
	tests := []struct {
		name         string
		observations []runtimematrix.Observation
	}{
		{
			name: "missing runtime",
			observations: []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			},
		},
		{
			name: "duplicate runtime",
			observations: []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			},
		},
		{
			name: "invalid observation does not leak version",
			observations: []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: false, Version: "/private/path", Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Build(fingerprint, tt.observations)
			if err == nil {
				t.Fatal("Build() error = nil, want error")
			}
			if !reflect.DeepEqual(plan, Plan{}) {
				t.Errorf("Build() plan = %#v, want zero Plan", plan)
			}
			if strings.Contains(err.Error(), "/private/path") {
				t.Errorf("error %q leaked observation data", err)
			}
		})
	}
}

func TestBuildIsDeterministicAndDoesNotMutateInputs(t *testing.T) {
	observations := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1.0.0", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Compatible},
	}
	original := append([]runtimematrix.Observation(nil), observations...)
	first, err := Build(fingerprint, observations)
	if err != nil {
		t.Fatalf("Build() first error = %v", err)
	}
	second, err := Build(fingerprint, []runtimematrix.Observation{observations[1], observations[2], observations[0]})
	if err != nil {
		t.Fatalf("Build() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("Build() depended on observation order: %#v != %#v", first, second)
	}
	if !reflect.DeepEqual(observations, original) {
		t.Errorf("Build() mutated observations: %#v, want %#v", observations, original)
	}
}

func TestBuildReturnsIndependentSlices(t *testing.T) {
	plan, err := Build(fingerprint, compatibleObservations())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	plan.Results[0].ID = "changed"
	plan.TransactionTargets[0] = "changed"
	fresh, err := Build(fingerprint, compatibleObservations())
	if err != nil {
		t.Fatalf("Build() fresh error = %v", err)
	}
	if fresh.Results[0].ID != runtimematrix.RuntimePi || fresh.TransactionTargets[0] != runtimematrix.RuntimePi {
		t.Errorf("fresh Build() shared mutable slices: %#v", fresh)
	}
}

func TestValidateAcceptsBuildOutputsWithoutMutation(t *testing.T) {
	tests := []struct {
		name         string
		observations []runtimematrix.Observation
	}{
		{name: "mixed", observations: []runtimematrix.Observation{
			{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Incompatible},
			{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: runtimematrix.Compatible},
		}},
		{name: "all", observations: compatibleObservations()},
		{name: "none", observations: []runtimematrix.Observation{
			{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
			{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Incompatible},
			{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := Build(fingerprint, tt.observations)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			original := clonePlan(plan)
			if err := Validate(plan); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !reflect.DeepEqual(plan, original) {
				t.Errorf("Validate() mutated plan: %#v, want %#v", plan, original)
			}
		})
	}
}

func TestValidateRejectsTamperedPlansWithoutLeaksOrMutation(t *testing.T) {
	all := mustBuild(t, compatibleObservations())
	none := mustBuild(t, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Incompatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
	})
	tests := []struct {
		name string
		plan Plan
	}{
		{name: "invalid fingerprint", plan: withPlan(all, func(plan *Plan) { plan.SnapshotFingerprint = "private-fingerprint" })},
		{name: "nil results", plan: withPlan(all, func(plan *Plan) { plan.Results = nil })},
		{name: "short results", plan: withPlan(all, func(plan *Plan) { plan.Results = plan.Results[:2] })},
		{name: "long results", plan: withPlan(all, func(plan *Plan) { plan.Results = append(plan.Results, plan.Results[0]) })},
		{name: "reordered results", plan: withPlan(all, func(plan *Plan) { plan.Results[0], plan.Results[1] = plan.Results[1], plan.Results[0] })},
		{name: "duplicate result", plan: withPlan(all, func(plan *Plan) { plan.Results[1] = plan.Results[0] })},
		{name: "unknown result", plan: withPlan(all, func(plan *Plan) { plan.Results[0].ID = "unknown-runtime" })},
		{name: "unknown outcome", plan: withPlan(all, func(plan *Plan) { plan.Results[0].Outcome = "unknown-outcome" })},
		{name: "unknown action", plan: withPlan(all, func(plan *Plan) { plan.Results[0].Action = "unknown-action" })},
		{name: "incoherent outcome", plan: withPlan(all, func(plan *Plan) { plan.Results[0].Outcome = runtimematrix.OutcomeAbsent })},
		{name: "incoherent action", plan: withPlan(all, func(plan *Plan) { plan.Results[0].Action = runtimematrix.Warn })},
		{name: "incoherent inclusion", plan: withPlan(all, func(plan *Plan) { plan.Results[0].IncludeInTransaction = false })},
		{name: "incoherent touch", plan: withPlan(all, func(plan *Plan) { plan.Results[0].TouchAllowed = false })},
		{name: "nil targets", plan: withPlan(all, func(plan *Plan) { plan.TransactionTargets = nil })},
		{name: "missing target", plan: withPlan(all, func(plan *Plan) { plan.TransactionTargets = plan.TransactionTargets[:2] })},
		{name: "reordered targets", plan: withPlan(all, func(plan *Plan) {
			plan.TransactionTargets[0], plan.TransactionTargets[1] = plan.TransactionTargets[1], plan.TransactionTargets[0]
		})},
		{name: "duplicate target", plan: withPlan(all, func(plan *Plan) { plan.TransactionTargets[1] = plan.TransactionTargets[0] })},
		{name: "unknown target", plan: withPlan(all, func(plan *Plan) { plan.TransactionTargets[0] = "unknown-runtime" })},
		{name: "extra target", plan: withPlan(all, func(plan *Plan) { plan.TransactionTargets = append(plan.TransactionTargets, "unknown-runtime") })},
		{name: "nonempty all-or-nothing mismatch", plan: withPlan(all, func(plan *Plan) { plan.AllOrNothing = false })},
		{name: "nonempty report-only mismatch", plan: withPlan(all, func(plan *Plan) { plan.ReportOnly = true })},
		{name: "empty all-or-nothing mismatch", plan: withPlan(none, func(plan *Plan) { plan.AllOrNothing = true })},
		{name: "empty report-only mismatch", plan: withPlan(none, func(plan *Plan) { plan.ReportOnly = false })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := clonePlan(tt.plan)
			err := Validate(tt.plan)
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if err.Error() != "adapter plan: invalid plan" {
				t.Errorf("Validate() error = %q, want generic error", err)
			}
			for _, forbidden := range []string{"private-fingerprint", "unknown-runtime", "unknown-outcome", "unknown-action", "1.0.0", "2.0.0", "3.0.0"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("Validate() error %q leaked %q", err, forbidden)
				}
			}
			if !reflect.DeepEqual(tt.plan, original) {
				t.Errorf("Validate() mutated plan: %#v, want %#v", tt.plan, original)
			}
		})
	}
}

func mustBuild(t *testing.T, observations []runtimematrix.Observation) Plan {
	t.Helper()
	plan, err := Build(fingerprint, observations)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	return plan
}

func withPlan(plan Plan, mutate func(*Plan)) Plan {
	clone := clonePlan(plan)
	mutate(&clone)
	return clone
}

func clonePlan(plan Plan) Plan {
	clone := plan
	if plan.Results != nil {
		clone.Results = append(make([]RuntimeResult, 0, len(plan.Results)), plan.Results...)
	}
	if plan.TransactionTargets != nil {
		clone.TransactionTargets = append(make([]runtimematrix.RuntimeID, 0, len(plan.TransactionTargets)), plan.TransactionTargets...)
	}
	return clone
}

func compatibleObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1.0.0", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: runtimematrix.Compatible},
	}
}
