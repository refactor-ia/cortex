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
	localUpdateTarget   runtimematrix.RuntimeID
	uncertifiedAdmitted []runtimematrix.RuntimeID
}

// LocalUpdateTarget returns the sole target produced by BuildLocalUpdate.
// The provenance is in-memory only and unavailable to strict plans.
func (plan Plan) LocalUpdateTarget() (runtimematrix.RuntimeID, bool) {
	return plan.localUpdateTarget, plan.localUpdateTarget != ""
}

// UncertifiedAdmitted returns a detached copy of the runtimes admitted without
// a certified version, in canonical order. The provenance is in-memory only and
// is derived by Build, never supplied by a caller.
func (plan Plan) UncertifiedAdmitted() []runtimematrix.RuntimeID {
	return append([]runtimematrix.RuntimeID{}, plan.uncertifiedAdmitted...)
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
	admitted := make([]runtimematrix.RuntimeID, 0, len(plan.Results))
	for index, id := range orderedIDs {
		result := plan.Results[index]
		if result.ID != id || !validResult(result, plan.localUpdateTarget, plan.uncertifiedAdmitted) {
			return errors.New("adapter plan: invalid plan")
		}
		if result.IncludeInTransaction {
			targets = append(targets, id)
		}
		if result.Outcome == runtimematrix.OutcomePresentUncertified {
			admitted = append(admitted, id)
		}
	}

	if len(plan.uncertifiedAdmitted) != len(admitted) {
		return errors.New("adapter plan: invalid plan")
	}
	for index, id := range admitted {
		if plan.uncertifiedAdmitted[index] != id {
			return errors.New("adapter plan: invalid plan")
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

	if plan.localUpdateTarget != "" && (!supportedLocalTarget(plan.localUpdateTarget) || len(targets) != 1 || targets[0] != plan.localUpdateTarget) {
		return errors.New("adapter plan: invalid plan")
	}
	if len(targets) > 0 && (!plan.AllOrNothing || plan.ReportOnly) {
		return errors.New("adapter plan: invalid plan")
	}
	if len(targets) == 0 && (plan.AllOrNothing || !plan.ReportOnly) {
		return errors.New("adapter plan: invalid plan")
	}
	return nil
}

func validResult(result RuntimeResult, localTarget runtimematrix.RuntimeID, admitted []runtimematrix.RuntimeID) bool {
	switch result.Outcome {
	case runtimematrix.OutcomePresentCompatible:
		if localTarget != "" && result.ID != localTarget {
			return result.Action == runtimematrix.Configure && !result.IncludeInTransaction && !result.TouchAllowed
		}
		return result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
	case runtimematrix.OutcomePresentUncertified:
		if !admits(admitted, result.ID) || result.Action != runtimematrix.Configure {
			return false
		}
		if localTarget != "" && result.ID != localTarget {
			return !result.IncludeInTransaction && !result.TouchAllowed
		}
		return result.IncludeInTransaction && result.TouchAllowed
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

func admits(admitted []runtimematrix.RuntimeID, id runtimematrix.RuntimeID) bool {
	for _, candidate := range admitted {
		if candidate == id {
			return true
		}
	}
	return false
}

// Build validates a catalog snapshot fingerprint and derives an adapter plan.
// Every present runtime with an identified version is admitted; the plan records
// which admissions were uncertified so callers can disclose them.
func Build(snapshotFingerprint string, observations []runtimematrix.Observation) (Plan, error) {
	matrix, err := runtimematrix.Decide(observations)
	if err != nil {
		return Plan{}, errors.New("adapter plan: invalid runtime observations")
	}
	return build(snapshotFingerprint, matrix, "")
}

// BuildLocalUpdate derives a one-target local plan without certifying the
// observed version or including any other runtime.
func BuildLocalUpdate(snapshotFingerprint string, observations []runtimematrix.Observation, target runtimematrix.RuntimeID) (Plan, error) {
	matrix, err := runtimematrix.DecideLocalUpdate(observations, target)
	if err != nil {
		return Plan{}, errors.New("adapter plan: invalid runtime observations")
	}
	return build(snapshotFingerprint, matrix, target)
}

func build(snapshotFingerprint string, matrix runtimematrix.Matrix, localTarget runtimematrix.RuntimeID) (Plan, error) {
	if !validFingerprint(snapshotFingerprint) {
		return Plan{}, errors.New("adapter plan: invalid snapshot fingerprint")
	}

	results := make([]RuntimeResult, 0, len(matrix.Decisions))
	targets := make([]runtimematrix.RuntimeID, 0, len(matrix.Decisions))
	admitted := make([]runtimematrix.RuntimeID, 0, len(matrix.Decisions))
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
		if decision.Outcome == runtimematrix.OutcomePresentUncertified {
			admitted = append(admitted, decision.ID)
		}
	}

	return Plan{
		SnapshotFingerprint: snapshotFingerprint,
		Results:             results,
		TransactionTargets:  targets,
		AllOrNothing:        len(targets) > 0,
		ReportOnly:          len(targets) == 0,
		localUpdateTarget:   localTarget,
		uncertifiedAdmitted: admitted,
	}, nil
}

func supportedLocalTarget(target runtimematrix.RuntimeID) bool {
	switch target {
	case runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode:
		return true
	default:
		return false
	}
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
