package installtxn

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"reflect"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
)

// deriveActorGuidanceAcceptedAfter is for a future composed transaction's
// finalizer, after fresh observation/shadow checks and exact final readback.
// Proof binds canonical prior state, installation/root identity and composed
// bytes; actors remain UserOwned, never generic full-hash ownership. Reloading
// verifies snapshot payloads, not historical source provenance. No writes occur.
func deriveActorGuidanceAcceptedAfter(candidate installplan.Plan, proof installobserve.ActorGuidanceClassification, observation installobserve.FilesystemObservation, snapshot filetxn.Snapshot) ([]filetxn.After, error) {
	if !proof.Matches(candidate, observation) {
		return nil, ErrInvalid
	}
	classified := proof.Result()
	if classified.StateAction() != ownership.Replace && classified.StateAction() != ownership.Unchanged {
		return nil, ErrConflict
	}
	for _, decision := range classified.ArtifactDecisions() {
		if decision.Kind == installstate.KindPiActor {
			if decision.ObservedOwnership != ownership.UserOwned || (decision.Action != ownership.Replace && decision.Action != ownership.Unchanged) {
				return nil, ErrConflict
			}
		} else if decision.Kind != installstate.KindSkill || !verifiedDecisionsReadySkill(decision) {
			return nil, ErrConflict
		}
	}
	sequenced, _, err := verifiedOperations(candidate, observation, classified)
	if err != nil {
		return nil, ErrInvalid
	}
	verified, err := filetxn.Open(filepath.Dir(snapshot.Dir), filepath.Base(snapshot.Dir))
	if err != nil || !reflect.DeepEqual(verified.Manifest, snapshot.Manifest) {
		return nil, ErrInvalid
	}
	operations := sequenced.flatten()
	if len(operations) != len(snapshot.Manifest.Entries) {
		return nil, ErrInvalid
	}
	entries := make(map[string]filetxn.Entry, len(snapshot.Manifest.Entries))
	for _, entry := range snapshot.Manifest.Entries {
		entries[entry.Path] = entry // Open rejects duplicate, unsafe and noncanonical entries.
	}
	after := make([]filetxn.After, 0, len(operations))
	for _, operation := range operations {
		var relative string
		var before, desired []byte
		var beforeMode, desiredMode fs.FileMode
		exists := true
		switch {
		case operation.Create != nil:
			relative, desired, desiredMode = operation.Create.Path, operation.Create.Data, operation.Create.Mode
		case operation.Replace != nil:
			replace := operation.Replace
			relative, before, beforeMode = replace.Path, replace.ExpectedData, replace.ExpectedMode
			desired, desiredMode = replace.Data, replace.Mode
		case operation.Remove != nil:
			remove := operation.Remove
			relative, before, beforeMode = remove.Path, remove.ExpectedData, remove.ExpectedMode
			exists = false
		default:
			return nil, ErrInvalid
		}
		entry, found := entries[relative]
		if !found || entry.Exists != (before != nil) || fs.FileMode(entry.Mode) != beforeMode {
			return nil, ErrInvalid
		}
		if entry.Exists {
			payload, err := verified.Payload(relative)
			if err != nil || !bytes.Equal(payload, before) {
				return nil, ErrInvalid
			}
		}
		value, err := filetxn.NewAfter(relative, exists, desired, desiredMode)
		if err != nil {
			return nil, ErrInvalid
		}
		after = append(after, value)
		delete(entries, relative)
	}
	return after, nil
}

func verifiedDecisionsReadySkill(decision installobserve.ArtifactDecision) bool {
	switch decision.Action {
	case ownership.Create, ownership.Replace, ownership.Remove, ownership.Unchanged:
		return decision.ObservedOwnership == ownership.CortexOwned
	case ownership.Preserve:
		return decision.ObservedOwnership != ownership.UserOwned
	}
	return false
}

// actorGuidanceRecoveryAfter only derives evidence for a future recovery caller.
// expectedID must come from trusted acceptance evidence, never be recomputed from
// an untrusted request: transactionID is a hash, not a MAC. Identical snapshot
// copies are equivalent. Recovery still needs RollbackRestart's live safe-path,
// exact-before/after, drift, directory and restoration checks; no route is opened.
func actorGuidanceRecoveryAfter(candidate installplan.Plan, proof installobserve.ActorGuidanceClassification, observation installobserve.FilesystemObservation, snapshot filetxn.Snapshot, expectedID TransactionID) ([]filetxn.After, error) {
	actualID, err := transactionID(candidate, snapshot)
	if !expectedID.Valid() || err != nil || actualID != expectedID {
		return nil, ErrInvalid
	}
	return deriveActorGuidanceAcceptedAfter(candidate, proof, observation, snapshot)
}
