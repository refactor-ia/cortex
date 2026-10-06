package installobserve

import (
	"strings"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/qarole"
)

// ObserveActorGuidanceShadows scans the existing bounded roots using composition
// evidence captured against the canonical v2 plan. Only its six exact actor
// preimages bypass the ordinary whole-file ownership gate. Conflicts fail closed;
// success is point-in-time evidence, not write, removal or precedence authority.
func ObserveActorGuidanceShadows(canonical installplan.Plan, observation FilesystemObservation, proof ActorGuidanceClassification, cwd string) (ShadowObservation, error) {
	if canonical.InstalledState().SchemaVersion() != 2 || !observation.MatchesCandidate(canonical) || !proof.Matches(proof.Candidate(), observation) {
		return ShadowObservation{}, shadowInvalid()
	}
	targets, ok := shadowTargets(canonical)
	if !ok {
		return ShadowObservation{}, shadowInvalid()
	}
	allowed := make(map[qarole.RoleID]bool, len(targets))
	for _, decision := range proof.Result().ArtifactDecisions() {
		if decision.Action == ownership.Conflict {
			return ShadowObservation{}, shadowInvalid()
		}
		if decision.Kind != installstate.KindPiActor {
			continue
		}
		role := qarole.RoleID(strings.TrimPrefix(decision.LogicalID, "actors/"))
		targetRole, target := targets["cortex-"+string(role)+".md"]
		exact, found := observation.Exact(decision.LogicalID)
		if !target || targetRole != role || allowed[role] || !found ||
			proof.preimages[decision.LogicalID] != hash(exact.Bytes()) || exact.Mode() != installplan.CanonicalFileMode ||
			decision.ObservedOwnership != ownership.UserOwned || (decision.Action != ownership.Unchanged && decision.Action != ownership.Replace) {
			return ShadowObservation{}, shadowInvalid()
		}
		allowed[role] = true
	}
	if len(allowed) != len(targets) {
		return ShadowObservation{}, shadowInvalid()
	}
	result, err := observeActorShadows(canonical, observation, cwd, allowed)
	if err != nil || !result.Clean() {
		return result, shadowInvalid()
	}
	return result, nil
}
