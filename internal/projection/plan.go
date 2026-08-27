package projection

import (
	"errors"

	"github.com/refactor-ia/cortex/internal/adapterplan"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// RuntimeResult records the adapter outcome and its projection refinement.
type RuntimeResult struct {
	ID                    runtimematrix.RuntimeID
	Outcome               runtimematrix.Outcome
	ProjectionResult      Result
	TranslationDisclosure string
	Action                runtimematrix.Action
	IncludeInTransaction  bool
	TouchAllowed          bool
}

// Plan is an immutable projection-refined runtime transaction plan.
type Plan struct {
	snapshotFingerprint string
	results             []RuntimeResult
	transactionTargets  []runtimematrix.RuntimeID
	allOrNothing        bool
	reportOnly          bool
}

// SnapshotFingerprint returns the catalog snapshot bound to the plan.
func (plan Plan) SnapshotFingerprint() string { return plan.snapshotFingerprint }

// Results returns a detached, non-nil copy of runtime reports.
func (plan Plan) Results() []RuntimeResult {
	return append([]RuntimeResult{}, plan.results...)
}

// TransactionTargets returns a detached, non-nil copy of transaction targets.
func (plan Plan) TransactionTargets() []runtimematrix.RuntimeID {
	return append([]runtimematrix.RuntimeID{}, plan.transactionTargets...)
}

// AllOrNothing reports whether the final transaction has targets.
func (plan Plan) AllOrNothing() bool { return plan.allOrNothing }

// ReportOnly reports whether no runtime can join the final transaction.
func (plan Plan) ReportOnly() bool { return plan.reportOnly }

// BuildPlan refines a valid adapter plan using bound projection assessments.
func BuildPlan(base adapterplan.Plan, assessments []Assessment) (Plan, error) {
	if adapterplan.Validate(base) != nil {
		return Plan{}, invalidPlan()
	}

	targets := make(map[runtimematrix.RuntimeID]struct{}, len(base.TransactionTargets))
	for _, id := range base.TransactionTargets {
		targets[id] = struct{}{}
	}
	if len(assessments) != len(targets) {
		return Plan{}, invalidPlan()
	}
	byRuntime := make(map[runtimematrix.RuntimeID]Assessment, len(assessments))
	for _, assessment := range assessments {
		id := assessment.RuntimeID()
		if _, target := targets[id]; !target || !validAssessment(assessment) || assessment.SnapshotFingerprint() != base.SnapshotFingerprint {
			return Plan{}, invalidPlan()
		}
		if _, duplicate := byRuntime[id]; duplicate {
			return Plan{}, invalidPlan()
		}
		byRuntime[id] = assessment
	}
	if len(byRuntime) != len(targets) {
		return Plan{}, invalidPlan()
	}

	results := make([]RuntimeResult, 0, len(base.Results))
	finalTargets := make([]runtimematrix.RuntimeID, 0, len(base.TransactionTargets))
	for _, baseResult := range base.Results {
		result := RuntimeResult{
			ID:                   baseResult.ID,
			Outcome:              baseResult.Outcome,
			Action:               baseResult.Action,
			IncludeInTransaction: baseResult.IncludeInTransaction,
			TouchAllowed:         baseResult.TouchAllowed,
		}
		if assessment, target := byRuntime[baseResult.ID]; target {
			result.ProjectionResult = assessment.Result()
			result.TranslationDisclosure = assessment.TranslationDisclosure()
			if assessment.Result() == Unrepresentable {
				result.Action = runtimematrix.Skip
				result.IncludeInTransaction = false
				result.TouchAllowed = false
			} else {
				finalTargets = append(finalTargets, baseResult.ID)
			}
		}
		results = append(results, result)
	}
	return Plan{
		snapshotFingerprint: base.SnapshotFingerprint,
		results:             results,
		transactionTargets:  finalTargets,
		allOrNothing:        len(finalTargets) > 0,
		reportOnly:          len(finalTargets) == 0,
	}, nil
}

func invalidPlan() error { return errors.New("projection plan: invalid input") }
