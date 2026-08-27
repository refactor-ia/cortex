// Package adapterplan builds pure runtime adapter transaction plans.
package adapterplan

import (
	"errors"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// RuntimeResult records one canonical runtime matrix decision.
type RuntimeResult struct {
	ID                   runtimematrix.RuntimeID
	Outcome              runtimematrix.Outcome
	Action               runtimematrix.Action
	IncludeInTransaction bool
	TouchAllowed         bool
}

// Plan binds canonical runtime decisions to one catalog snapshot.
type Plan struct {
	SnapshotFingerprint string
	Results             []RuntimeResult
	TransactionTargets  []runtimematrix.RuntimeID
	AllOrNothing        bool
	ReportOnly          bool
}

// Validate confirms that a plan retains the canonical adapter contract shape.
func Validate(plan Plan) error {
	if !validFingerprint(plan.SnapshotFingerprint) || plan.Results == nil || len(plan.Results) != 3 {
		return errors.New("adapter plan: invalid plan")
	}

	orderedIDs := []runtimematrix.RuntimeID{
		runtimematrix.RuntimePi,
		runtimematrix.RuntimeOpenCode,
		runtimematrix.RuntimeClaudeCode,
	}
	targets := make([]runtimematrix.RuntimeID, 0, len(plan.Results))
	for index, id := range orderedIDs {
		result := plan.Results[index]
		if result.ID != id || !validResult(result) {
			return errors.New("adapter plan: invalid plan")
		}
		if result.IncludeInTransaction {
			targets = append(targets, id)
		}
	}

	if plan.TransactionTargets == nil || len(plan.TransactionTargets) != len(targets) {
		return errors.New("adapter plan: invalid plan")
	}
	for index, id := range targets {
		if plan.TransactionTargets[index] != id {
			return errors.New("adapter plan: invalid plan")
		}
	}

	if len(targets) > 0 && (!plan.AllOrNothing || plan.ReportOnly) {
		return errors.New("adapter plan: invalid plan")
	}
	if len(targets) == 0 && (plan.AllOrNothing || !plan.ReportOnly) {
		return errors.New("adapter plan: invalid plan")
	}
	return nil
}

func validResult(result RuntimeResult) bool {
	switch result.Outcome {
	case runtimematrix.OutcomePresentCompatible:
		return result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
	case runtimematrix.OutcomeAbsent:
		return result.Action == runtimematrix.Warn && !result.IncludeInTransaction && !result.TouchAllowed
	case runtimematrix.OutcomeKnownIncompatible:
		return result.Action == runtimematrix.Skip && !result.IncludeInTransaction && !result.TouchAllowed
	case runtimematrix.OutcomeUnknownVersion:
		return result.Action == runtimematrix.Warn && !result.IncludeInTransaction && !result.TouchAllowed
	default:
		return false
	}
}

// Build validates a catalog snapshot fingerprint and derives a pure adapter plan.
func Build(snapshotFingerprint string, observations []runtimematrix.Observation) (Plan, error) {
	if !validFingerprint(snapshotFingerprint) {
		return Plan{}, errors.New("adapter plan: invalid snapshot fingerprint")
	}

	matrix, err := runtimematrix.Decide(observations)
	if err != nil {
		return Plan{}, errors.New("adapter plan: invalid runtime observations")
	}

	results := make([]RuntimeResult, 0, len(matrix.Decisions))
	targets := make([]runtimematrix.RuntimeID, 0, len(matrix.Decisions))
	for _, decision := range matrix.Decisions {
		results = append(results, RuntimeResult{
			ID:                   decision.ID,
			Outcome:              decision.Outcome,
			Action:               decision.Action,
			IncludeInTransaction: decision.IncludeInTransaction,
			TouchAllowed:         decision.TouchAllowed,
		})
		if decision.IncludeInTransaction {
			targets = append(targets, decision.ID)
		}
	}

	return Plan{
		SnapshotFingerprint: snapshotFingerprint,
		Results:             results,
		TransactionTargets:  targets,
		AllOrNothing:        len(targets) > 0,
		ReportOnly:          len(targets) == 0,
	}, nil
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
