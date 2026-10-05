package installobserve

import (
	"bytes"
	"reflect"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
)

// ActorGuidanceClassification binds detached update decisions to internally
// composed bytes and captured evidence, including prior state and all six actor
// preimage digests, modes and paths. It grants no mutation or removal authority.
// installcoord/apply and admission still reject v3; fresh root safety and shadow
// checks remain required there. Ordinary classification/shadow gates are unchanged.
// Matches accepts the canonical-plan capture, not an observation of the v3 plan.
// Actor decisions deliberately do not claim whole-file Cortex ownership.
type ActorGuidanceClassification struct {
	candidate   installplan.Plan
	observation FilesystemObservation
	preimages   map[string]string
	result      Result
	verified    bool
}

func (proof ActorGuidanceClassification) Candidate() installplan.Plan { return proof.candidate }
func (proof ActorGuidanceClassification) Result() Result              { return proof.result }

// Matches rejects zero evidence and any changed candidate, prior state, slot,
// preimage bytes/mode or path (paths are bound by the two candidate bindings).
func (proof ActorGuidanceClassification) Matches(candidate installplan.Plan, observation FilesystemObservation) bool {
	binding, valid := candidateBinding(candidate)
	expected, expectedValid := candidateBinding(proof.candidate)
	return proof.verified && valid && expectedValid && binding == expected && reflect.DeepEqual(proof.observation, observation)
}

func (proof ActorGuidanceClassification) validPrior(prior *PriorState, candidate installplan.Plan) bool {
	if prior == nil || proof.observation.prior == nil || !proof.Matches(candidate, proof.observation) {
		return false
	}
	encoded, err := installstate.Encode(prior.Manifest)
	return err == nil && prior.StateSHA256 == proof.observation.prior.StateSHA256 && bytes.Equal(encoded, proof.observation.exact["state/install-state"].bytes)
}

// ClassifyActorGuidance requires a trusted canonical desired v2 plan, a captured
// Observe result bound to it, and explicit legacy adoption. The bridge and builder
// authenticate canonical legacy or v3 effective identity before granting actor
// update/no-op evidence. Skills retain ordinary drift/no-takeover semantics.
// No caller-provided composed candidate or boolean ownership bypass is accepted.
func ClassifyActorGuidance(canonical installplan.Plan, observation FilesystemObservation, adoptLegacy bool) (ActorGuidanceClassification, error) {
	prior, actors, err := observation.ActorGuidanceInputs(canonical)
	if err != nil {
		return ActorGuidanceClassification{}, err
	}
	candidate, preimages, err := installplan.BuildActorGuidance(canonical, prior, actors, adoptLegacy)
	if err != nil {
		return ActorGuidanceClassification{}, err
	}
	proof := ActorGuidanceClassification{candidate: candidate, observation: observation, preimages: preimages, verified: true}
	captured := observation.PriorState()
	previous, valid := validPriorWithGuidance(*captured, candidate, candidate.InstalledState(), &proof)
	if !valid || !validExactObservationWithGuidance(candidate, observation, captured, &proof) {
		return ActorGuidanceClassification{}, invalid()
	}
	for id := range previous {
		if exact, found := observation.exact[id]; found && exact.mode != installplan.CanonicalFileMode {
			previous[id] = nonCanonicalOwnershipHash
		}
	}
	proof.result = classifyWithGuidance(candidate.InstalledState(), hash(candidate.StateJSON()), captured, previous, observation.Slots(), &proof)
	return proof, nil
}
