package installobserve

import (
	"bytes"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qarole"
)

// ActorGuidanceInputs detaches captured prior ownership and the six existing
// actors for BuildActorGuidance. It performs no I/O or composition and grants no
// admission, apply, or deletion authority. Legacy adoption/canonical proof and
// v3 effective-byte proof remain the builder's responsibility; apply must bind
// fresh safe observations to the composed candidate and its expected preimages.
func (observation FilesystemObservation) ActorGuidanceInputs(candidate installplan.Plan) (installplan.ActorGuidancePrior, []installplan.ActorGuidanceObservation, error) {
	fail := func() (installplan.ActorGuidancePrior, []installplan.ActorGuidanceObservation, error) {
		return installplan.ActorGuidancePrior{}, nil, filesystemInvalid()
	}
	if !observation.MatchesCandidate(candidate) || candidate.InstalledState().SchemaVersion() != 2 || observation.prior == nil {
		return fail()
	}
	if _, present := candidate.Bundle(); !present {
		return fail()
	}
	state, present := observation.Exact("state/install-state")
	if !present || state.Mode() != installplan.CanonicalFileMode {
		return fail()
	}
	// Decode captured canonical bytes anew so even the returned typed state is
	// detached from the private prior. Never reconstruct state from actor bytes.
	prior, err := installstate.Decode(state.bytes)
	encoded, encodeErr := installstate.Encode(prior)
	capturedPrior, priorErr := installstate.Encode(observation.prior.Manifest)
	if err != nil || encodeErr != nil || priorErr != nil || !bytes.Equal(encoded, state.bytes) || !bytes.Equal(capturedPrior, state.bytes) ||
		observation.prior.StateSHA256 != hash(state.bytes) || (prior.SchemaVersion() != 2 && prior.SchemaVersion() != 3) ||
		prior.RuntimeID() != candidate.RuntimeID() || prior.RootKind() != candidate.RootKind() ||
		prior.InstallationID() != candidate.InstalledState().InstallationID() {
		return fail()
	}
	previous := make(map[string]installstate.Artifact)
	for _, artifact := range prior.Artifacts() {
		if artifact.Kind() == installstate.KindPiActor {
			previous[artifact.LogicalID()] = artifact
		}
	}
	slots := make(map[string]SlotObservation, len(observation.slots))
	for _, slot := range observation.slots {
		if _, duplicate := slots[slot.LogicalID]; duplicate {
			return fail()
		}
		slots[slot.LogicalID] = slot
	}
	actors := make([]installplan.ActorGuidanceObservation, 0, len(qarole.Catalog()))
	for _, file := range candidate.Files() {
		if file.Role() != "actor" {
			continue
		}
		owned, found := previous[file.LogicalID()]
		slot, observed := slots[file.LogicalID()]
		exact, available := observation.Exact(file.LogicalID())
		if !found || !observed || !slot.Present || !available || exact.Mode() != installplan.CanonicalFileMode || slot.SHA256 != hash(exact.bytes) ||
			owned.RelativePath() != file.RelativePath() || owned.LogicalID() != "actors/"+string(owned.RoleID()) {
			return fail()
		}
		// Valid v2/v3 manifests enforce closed role/path/contract/installation
		// identities. MatchesCandidate validates the desired file/path mapping.
		actors = append(actors, installplan.ActorGuidanceObservation{
			LogicalID: file.LogicalID(), RootPath: candidate.RootPath(), RelativePath: file.RelativePath(),
			AbsolutePath: file.AbsolutePath(), Mode: exact.Mode(), Content: exact.Bytes(),
		})
	}
	if len(actors) != len(qarole.Catalog()) || len(previous) != len(actors) {
		return fail()
	}
	return installplan.ActorGuidancePrior{RootPath: candidate.RootPath(), State: prior}, actors, nil
}
