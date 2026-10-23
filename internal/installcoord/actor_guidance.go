package installcoord

import (
	"errors"
	"io/fs"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// ActorGuidanceRefusalStage names the preflight gate that refused an
// actor-guidance update, so a caller can report which asset failed verification
// instead of a bare reason token (issue #373). The stage is evidence only: it
// grants no write, removal, or admission authority.
type ActorGuidanceRefusalStage uint8

const (
	// ActorGuidanceRefusedEvidence means the captured evidence itself did not
	// verify: it was invalid, stale, unadopted, or tampered with.
	ActorGuidanceRefusedEvidence ActorGuidanceRefusalStage = iota + 1
	// ActorGuidanceRefusedConflict means classified artifact decisions
	// conflicted with the candidate.
	ActorGuidanceRefusedConflict
	// ActorGuidanceRefusedShadow means the bounded shadow scan was not clean.
	ActorGuidanceRefusedShadow
)

// ActorGuidanceRefusalCause names why one captured asset no longer matches the
// installation the update would refresh.
type ActorGuidanceRefusalCause uint8

const (
	// ActorGuidanceContentDrift means the captured bytes are not the content
	// this installation owns: they differ from the recorded digest, or the
	// captured asset carries no recorded ownership at all.
	ActorGuidanceContentDrift ActorGuidanceRefusalCause = iota + 1
	// ActorGuidanceModeDrift means the captured file mode differs from the
	// canonical mode the asset is installed with.
	ActorGuidanceModeDrift
)

// ActorGuidanceRefusalAsset names one captured asset whose identity no longer
// matches, with its runtime-relative path, so a refusal can name the file and
// the drift instead of only the reason token. Cause is the single reason the
// asset refused verification; ModeDrift records the additional, independent
// fact that its file mode also diverged, so a conflict never silently drops the
// mode evidence the capture already carries.
type ActorGuidanceRefusalAsset struct {
	LogicalID     string
	RelativePath  string
	Cause         ActorGuidanceRefusalCause
	ModeDrift     bool
	ObservedMode  fs.FileMode
	CanonicalMode fs.FileMode
}

// ActorGuidanceRefusal is detached point-in-time evidence for one refused
// actor-guidance preflight: the refusing stage plus the assets and bounded
// shadow conflicts captured up to that point. It never becomes planning, apply,
// or deletion authority, and it is computed only after the refusal is decided,
// so it can neither admit nor reject on its own.
type ActorGuidanceRefusal struct {
	stage   ActorGuidanceRefusalStage
	assets  []ActorGuidanceRefusalAsset
	shadows []installobserve.ShadowConflict
}

// Valid reports whether this record describes an actual refusal. The zero value
// describes a preflight that was not refused.
func (refusal ActorGuidanceRefusal) Valid() bool { return refusal.stage != 0 }

// Stage returns the preflight gate that refused.
func (refusal ActorGuidanceRefusal) Stage() ActorGuidanceRefusalStage { return refusal.stage }

// Assets returns the detached captured assets that no longer match, in
// canonical slot order.
func (refusal ActorGuidanceRefusal) Assets() []ActorGuidanceRefusalAsset {
	return append([]ActorGuidanceRefusalAsset{}, refusal.assets...)
}

// Shadows returns the detached bounded role/location shadow conflicts that
// refused the update.
func (refusal ActorGuidanceRefusal) Shadows() []installobserve.ShadowConflict {
	return append([]installobserve.ShadowConflict{}, refusal.shadows...)
}

// ActorGuidanceReport retains point-in-time planning evidence, never write or
// deletion authority. Composed restart recovery remains unsupported.
type ActorGuidanceReport struct {
	report  Report
	proof   installobserve.ActorGuidanceClassification
	refusal ActorGuidanceRefusal
}

func (report ActorGuidanceReport) Report() Report { return report.report }
func (report ActorGuidanceReport) Proof() installobserve.ActorGuidanceClassification {
	return report.proof
}

// Refusal returns the detached refusal evidence. A refused preflight keeps its
// report and proof unusable; only the diagnostic record is populated.
func (report ActorGuidanceReport) Refusal() ActorGuidanceRefusal { return report.refusal }

// PreflightActorGuidance plans one explicit local Pi update from a canonical v2
// Unit. Legacy guidance adoption is opt-in; it is not an ownership bypass.
// Actions come only from ClassifyActorGuidance, with the same local runtime
// policy as PreflightLocalUpdate. Shadow or skill conflicts return no usable
// report/proof but do return the stage and the captured assets that refused, so
// callers can name the conflicting files. No target writes occur; later apply
// must freshly revalidate the canonical unit, exact composed candidate/proof,
// and cwd. This is not durable recovery evidence, admission, or public mutation
// routing.
func PreflightActorGuidance(observations []runtimematrix.Observation, unit Unit, adoptLegacy bool, cwd string) (ActorGuidanceReport, error) {
	if unit.Plan.RuntimeID() != runtimematrix.RuntimePi || unit.Plan.InstalledState().SchemaVersion() != 2 || !validUnit(unit) {
		return refusedActorGuidance(ActorGuidanceRefusedEvidence, recordedDriftAssets(unit), ErrInvalid)
	}
	matrix, err := runtimematrix.DecideLocalUpdate(observations, unit.Plan.RuntimeID())
	if err != nil || !matrix.HasCompatible {
		return refusedActorGuidance(ActorGuidanceRefusedEvidence, recordedDriftAssets(unit), ErrInvalid)
	}
	proof, err := installobserve.ClassifyActorGuidance(unit.Plan, unit.Observation, adoptLegacy)
	if err != nil {
		return refusedActorGuidance(ActorGuidanceRefusedEvidence, recordedDriftAssets(unit), errors.Join(ErrInvalid, err))
	}
	classified := proof.Result()
	if !classifiedReady(classified) {
		return refusedActorGuidance(ActorGuidanceRefusedConflict, conflictAssets(unit.Plan, classified), ErrInvalid)
	}
	shadows, err := installobserve.ObserveActorGuidanceShadows(unit.Plan, unit.Observation, proof, cwd)
	if err != nil || !shadows.Clean() {
		return ActorGuidanceReport{refusal: ActorGuidanceRefusal{stage: ActorGuidanceRefusedShadow, shadows: shadows.Conflicts()}}, errors.Join(ErrInvalid, err)
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

// refusedActorGuidance returns a refused report with its diagnostic record and
// the refusal error, so usable report/proof evidence stays empty by contract.
func refusedActorGuidance(stage ActorGuidanceRefusalStage, assets []ActorGuidanceRefusalAsset, err error) (ActorGuidanceReport, error) {
	return ActorGuidanceReport{refusal: ActorGuidanceRefusal{stage: stage, assets: assets}}, err
}

// conflictAssets names the classified artifact decisions that conflicted with
// the candidate, so a conflict refusal reports exactly what refused instead of
// every unrelated drift the same installation carries.
func conflictAssets(plan installplan.Plan, classified installobserve.Result) []ActorGuidanceRefusalAsset {
	relative := make(map[string]string, len(plan.Files()))
	canonical := make(map[string]fs.FileMode, len(plan.Files()))
	for _, file := range plan.Files() {
		relative[file.LogicalID()] = file.RelativePath()
		canonical[file.LogicalID()] = file.DesiredMode().Perm()
	}
	assets := make([]ActorGuidanceRefusalAsset, 0, len(classified.ArtifactDecisions()))
	for _, decision := range classified.ArtifactDecisions() {
		if decision.Action != ownership.Conflict {
			continue
		}
		assets = append(assets, ActorGuidanceRefusalAsset{
			LogicalID:     decision.LogicalID,
			RelativePath:  relative[decision.LogicalID],
			Cause:         ActorGuidanceContentDrift,
			ModeDrift:     decision.ModeDrift,
			ObservedMode:  decision.ObservedMode.Perm(),
			CanonicalMode: canonical[decision.LogicalID],
		})
	}
	return assets
}

// recordedDriftAssets names each captured asset whose identity no longer
// matches the recorded installation. It is used when classification itself
// refused, so it reads captured evidence only, never the filesystem, and it
// distinguishes only the two causes spec S2 requires: mode drift from the
// canonical mode, and content drift from the recorded digest. An asset the
// recorded ledger does not own has no digest to compare, so it is reported as
// content drift instead of being dropped; without any recorded ledger nothing
// can be verified as owned, so every present candidate asset is reported.
func recordedDriftAssets(unit Unit) []ActorGuidanceRefusalAsset {
	desired := make(map[string]installplan.File, len(unit.Plan.Files()))
	for _, file := range unit.Plan.Files() {
		desired[file.LogicalID()] = file
	}
	var recorded map[string]string
	if prior := unit.Observation.PriorState(); prior != nil {
		recorded = make(map[string]string, len(prior.Manifest.Artifacts()))
		for _, artifact := range prior.Manifest.Artifacts() {
			recorded[artifact.LogicalID()] = artifact.SHA256()
		}
	}
	assets := make([]ActorGuidanceRefusalAsset, 0, len(desired))
	for _, slot := range unit.Observation.Slots() {
		file, wanted := desired[slot.LogicalID]
		if !wanted || !slot.Present {
			continue
		}
		captured, found := unit.Observation.Exact(slot.LogicalID)
		if !found {
			continue
		}
		observedMode, canonicalMode := captured.Mode().Perm(), file.DesiredMode().Perm()
		asset := ActorGuidanceRefusalAsset{LogicalID: slot.LogicalID, RelativePath: file.RelativePath(), ModeDrift: observedMode != canonicalMode, ObservedMode: observedMode, CanonicalMode: canonicalMode}
		switch ownerHash, owned := recorded[slot.LogicalID]; {
		case asset.ModeDrift:
			asset.Cause = ActorGuidanceModeDrift
		case !owned || ownerHash != slot.SHA256:
			asset.Cause = ActorGuidanceContentDrift
		default:
			continue
		}
		assets = append(assets, asset)
	}
	return assets
}
