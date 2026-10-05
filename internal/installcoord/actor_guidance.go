package installcoord

import (
	"errors"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// ActorGuidanceReport retains point-in-time planning evidence, never write or
// deletion authority. Composed restart recovery remains unsupported.
type ActorGuidanceReport struct {
	report Report
	proof  installobserve.ActorGuidanceClassification
}

func (report ActorGuidanceReport) Report() Report { return report.report }
func (report ActorGuidanceReport) Proof() installobserve.ActorGuidanceClassification {
	return report.proof
}

// PreflightActorGuidance plans one explicit local Pi update from a canonical v2
// Unit. Legacy guidance adoption is opt-in; it is not an ownership bypass.
// Actions come only from ClassifyActorGuidance, with the same local runtime
// policy as PreflightLocalUpdate. Shadow or skill conflicts return no usable
// report/proof. No target writes occur; later apply must freshly revalidate the
// canonical unit, exact composed candidate/proof, and cwd. This is not durable
// recovery evidence, admission, or public mutation routing.
func PreflightActorGuidance(observations []runtimematrix.Observation, unit Unit, adoptLegacy bool, cwd string) (ActorGuidanceReport, error) {
	if unit.Plan.RuntimeID() != runtimematrix.RuntimePi || unit.Plan.InstalledState().SchemaVersion() != 2 || !validUnit(unit) {
		return ActorGuidanceReport{}, ErrInvalid
	}
	matrix, err := runtimematrix.DecideLocalUpdate(observations, unit.Plan.RuntimeID())
	if err != nil || !matrix.HasCompatible {
		return ActorGuidanceReport{}, ErrInvalid
	}
	proof, err := installobserve.ClassifyActorGuidance(unit.Plan, unit.Observation, adoptLegacy)
	if err != nil {
		return ActorGuidanceReport{}, errors.Join(ErrInvalid, err)
	}
	classified := proof.Result()
	if !classifiedReady(classified) {
		return ActorGuidanceReport{}, ErrInvalid
	}
	shadows, err := installobserve.ObserveActorGuidanceShadows(unit.Plan, unit.Observation, proof, cwd)
	if err != nil || !shadows.Clean() {
		return ActorGuidanceReport{}, errors.Join(ErrInvalid, err)
	}
	statuses := make([]Status, 0, len(matrix.Decisions))
	for _, decision := range matrix.Decisions {
		status := Status{RuntimeID: decision.ID, Outcome: decision.Outcome, Action: decision.Action}
		if decision.IncludeInTransaction {
			status.actions = classifiedActions(classified)
			status.ready = classifiedReady(classified)
		}
		statuses = append(statuses, status)
	}
	return ActorGuidanceReport{report: Report{statuses: statuses, ready: classifiedReady(classified)}, proof: proof}, nil
}
