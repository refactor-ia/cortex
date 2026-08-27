package projection

import (
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

func TestBuildPlanRefinesExactAndTranslatedTargetsInCanonicalOrder(t *testing.T) {
	base := projectionBase(t, compatibleProjectionObservations())
	plan, err := BuildPlan(base, []Assessment{
		projectionAssessment(t, runtimematrix.RuntimeClaudeCode, Exact, ""),
		projectionAssessment(t, runtimematrix.RuntimeOpenCode, Translated, "equivalent OpenCode routing"),
		projectionAssessment(t, runtimematrix.RuntimePi, Exact, ""),
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if got, want := plan.TransactionTargets(), []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}; !reflect.DeepEqual(got, want) {
		t.Errorf("TransactionTargets() = %#v, want %#v", got, want)
	}
	results := plan.Results()
	if results[1].ProjectionResult != Translated || results[1].TranslationDisclosure != "equivalent OpenCode routing" {
		t.Errorf("translated result = %#v, want preserved disclosure", results[1])
	}
	if !plan.AllOrNothing() || plan.ReportOnly() {
		t.Errorf("flags = %t/%t, want true/false", plan.AllOrNothing(), plan.ReportOnly())
	}
}

func TestBuildPlanUnrepresentableSkipsWithoutChangingAdapterOutcome(t *testing.T) {
	base := projectionBase(t, compatibleProjectionObservations())
	plan, err := BuildPlan(base, []Assessment{
		projectionAssessment(t, runtimematrix.RuntimePi, Exact, ""),
		projectionAssessment(t, runtimematrix.RuntimeOpenCode, Unrepresentable, ""),
		projectionAssessment(t, runtimematrix.RuntimeClaudeCode, Exact, ""),
	})
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result := plan.Results()[1]
	if result.Outcome != runtimematrix.OutcomePresentCompatible || result.ProjectionResult != Unrepresentable || result.Action != runtimematrix.Skip || result.IncludeInTransaction || result.TouchAllowed {
		t.Errorf("unrepresentable result = %#v", result)
	}
	if got, want := plan.TransactionTargets(), []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode}; !reflect.DeepEqual(got, want) {
		t.Errorf("TransactionTargets() = %#v, want %#v", got, want)
	}
}

func TestBuildPlanReportOnlyCases(t *testing.T) {
	tests := []struct {
		name        string
		base        adapterplan.Plan
		assessments []Assessment
	}{
		{"all unrepresentable", projectionBase(t, compatibleProjectionObservations()), []Assessment{
			projectionAssessment(t, runtimematrix.RuntimePi, Unrepresentable, ""),
			projectionAssessment(t, runtimematrix.RuntimeOpenCode, Unrepresentable, ""),
			projectionAssessment(t, runtimematrix.RuntimeClaudeCode, Unrepresentable, ""),
		}},
		{"no compatible", projectionBase(t, noCompatibleProjectionObservations()), []Assessment{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildPlan(tt.base, tt.assessments)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			if targets := plan.TransactionTargets(); targets == nil || len(targets) != 0 || plan.AllOrNothing() || !plan.ReportOnly() {
				t.Errorf("report-only plan targets/flags = %#v/%t/%t", targets, plan.AllOrNothing(), plan.ReportOnly())
			}
			if results := plan.Results(); results == nil || len(results) != 3 {
				t.Errorf("Results() = %#v, want three reports", results)
			}
		})
	}
}

func TestBuildPlanOrderInputsAndOutputsAreImmutable(t *testing.T) {
	base := projectionBase(t, compatibleProjectionObservations())
	baseOriginal := cloneProjectionBase(base)
	assessments := []Assessment{
		projectionAssessment(t, runtimematrix.RuntimeClaudeCode, Exact, ""),
		projectionAssessment(t, runtimematrix.RuntimePi, Exact, ""),
		projectionAssessment(t, runtimematrix.RuntimeOpenCode, Translated, "equivalent"),
	}
	originalAssessments := append([]Assessment(nil), assessments...)
	first, err := BuildPlan(base, assessments)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	second, err := BuildPlan(base, []Assessment{assessments[1], assessments[2], assessments[0]})
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Errorf("BuildPlan() order-independent output = %#v / %#v, error = %v", first, second, err)
	}
	results, targets := first.Results(), first.TransactionTargets()
	results[0].ID, targets[0] = "changed", "changed"
	if first.Results()[0].ID != runtimematrix.RuntimePi || first.TransactionTargets()[0] != runtimematrix.RuntimePi {
		t.Error("accessors exposed mutable plan state")
	}
	if !reflect.DeepEqual(base, baseOriginal) || !reflect.DeepEqual(assessments, originalAssessments) {
		t.Error("BuildPlan() mutated caller input")
	}
}

func TestBuildPlanRejectsInvalidInputWithoutLeaks(t *testing.T) {
	base := projectionBase(t, []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
	})
	pi := projectionAssessment(t, runtimematrix.RuntimePi, Exact, "")
	wrongFingerprint := Assessment{runtimeID: runtimematrix.RuntimePi, snapshotFingerprint: strings.Repeat("b", 64), result: Exact}
	tests := []struct {
		name        string
		base        adapterplan.Plan
		assessments []Assessment
	}{
		{"missing", base, nil},
		{"duplicate", base, []Assessment{pi, pi}},
		{"non-target", base, []Assessment{projectionAssessment(t, runtimematrix.RuntimeOpenCode, Exact, "")}},
		{"zero", base, []Assessment{{}}},
		{"wrong runtime", base, []Assessment{{runtimeID: "unknown", snapshotFingerprint: testFingerprint, result: Exact}}},
		{"wrong fingerprint", base, []Assessment{wrongFingerprint}},
		{"tampered base", tamperedProjectionBase(base), []Assessment{pi}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildPlan(tt.base, tt.assessments)
			if err == nil || !reflect.DeepEqual(plan, Plan{}) || err.Error() != "projection plan: invalid input" {
				t.Errorf("BuildPlan() = %#v, %v; want zero plan and generic error", plan, err)
			}
			for _, secret := range []string{testFingerprint, "equivalent", "unknown", "1"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %q", secret, err)
				}
			}
		})
	}
}

func projectionAssessment(t *testing.T, id runtimematrix.RuntimeID, result Result, disclosure string) Assessment {
	t.Helper()
	assessment, err := NewAssessment(id, testFingerprint, result, disclosure)
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	return assessment
}

func projectionBase(t *testing.T, observations []runtimematrix.Observation) adapterplan.Plan {
	t.Helper()
	base, err := adapterplan.Build(testFingerprint, observations)
	if err != nil {
		t.Fatalf("adapterplan.Build() error = %v", err)
	}
	return base
}

func cloneProjectionBase(plan adapterplan.Plan) adapterplan.Plan {
	clone := plan
	clone.Results = append([]adapterplan.RuntimeResult(nil), plan.Results...)
	clone.TransactionTargets = append([]runtimematrix.RuntimeID(nil), plan.TransactionTargets...)
	return clone
}

func tamperedProjectionBase(plan adapterplan.Plan) adapterplan.Plan {
	clone := cloneProjectionBase(plan)
	clone.Results[0].Action = runtimematrix.Warn
	return clone
}

func compatibleProjectionObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: true, Version: "1", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2", Compatibility: runtimematrix.Compatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Version: "3", Compatibility: runtimematrix.Compatible},
	}
}

func noCompatibleProjectionObservations() []runtimematrix.Observation {
	return []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2", Compatibility: runtimematrix.Incompatible},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
	}
}
