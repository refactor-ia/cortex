package installtxn

import (
	"bytes"
	"errors"
	"os"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/safepath"
)

// ApplyActorGuidance applies only the exact composed candidate bound to captured
// evidence in proof. It uses real transaction dependencies and freshly checks
// the full canonical observation and actor shadows before mutation and readback.
// Readiness alone grants no authority. In-process rollback is supported;
// composed restart recovery and external-guidance-preserving uninstall are not.
func ApplyActorGuidance(canonical installplan.Plan, observation installobserve.FilesystemObservation, proof installobserve.ActorGuidanceClassification, cwd, backupRoot, backupName string) (Result, error) {
	return applyActorGuidance(canonical, observation, proof, cwd, backupRoot, backupName)
}

func applyActorGuidance(canonical installplan.Plan, observation installobserve.FilesystemObservation, proof installobserve.ActorGuidanceClassification, cwd, backupRoot, backupName string) (Result, error) {
	return applyActorGuidanceWith(canonical, observation, proof, cwd, backupRoot, backupName, filetxn.ApplyOperationsWithDirectoriesAndFinalize)
}

func applyActorGuidanceWith(canonical installplan.Plan, observation installobserve.FilesystemObservation, proof installobserve.ActorGuidanceClassification, cwd, backupRoot, backupName string, apply applyVerifiedTransaction) (Result, error) {
	candidate, classified := proof.Candidate(), proof.Result()
	if apply == nil || canonical.InstalledState().SchemaVersion() != 2 || !canonicalRoot(canonical.RootPath()) || !validCWD(cwd) || !observation.MatchesCandidate(canonical) || !proof.Matches(candidate, observation) {
		return Result{}, ErrInvalid
	}
	if classified.StateAction() != ownership.Replace && classified.StateAction() != ownership.Unchanged {
		return Result{}, ErrConflict
	}
	for _, decision := range classified.ArtifactDecisions() {
		if decision.Kind == installstate.KindPiActor {
			if decision.ObservedOwnership != ownership.UserOwned || (decision.Action != ownership.Replace && decision.Action != ownership.Unchanged) {
				return Result{}, ErrConflict
			}
		} else if decision.Kind != installstate.KindSkill || !verifiedDecisionsReadySkill(decision) {
			return Result{}, ErrConflict
		}
	}
	sequenced, result, err := verifiedOperations(candidate, observation, classified)
	if err != nil {
		return Result{}, errors.Join(ErrInvalid, err)
	}
	operations := sequenced.flatten()
	current, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
	if err != nil || !proof.Matches(candidate, current) {
		return Result{}, errors.Join(ErrInvalid, err)
	}
	shadows, err := installobserve.ObserveActorGuidanceShadows(canonical, current, proof, cwd)
	if err != nil || !shadows.Clean() {
		return Result{}, errors.Join(ErrConflict, err)
	}
	verify := func() error {
		fresh, err := installobserve.Observe(canonical, installobserve.DefaultOptions())
		if err != nil {
			return err
		}
		for _, file := range candidate.Files() {
			exact, found := fresh.Exact(file.LogicalID())
			if !found || exact.Mode() != file.DesiredMode() || !bytes.Equal(exact.Bytes(), file.Content()) {
				return ErrFailed
			}
		}
		for _, operation := range operations {
			if operation.Remove != nil {
				target, err := safepath.Resolve(candidate.RootPath(), operation.Remove.Path)
				if err != nil {
					return err
				}
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					return ErrFailed
				}
			}
		}
		repeat, err := installobserve.ClassifyActorGuidance(canonical, fresh, false)
		if err != nil || !repeat.Matches(candidate, fresh) {
			return errors.Join(ErrFailed, err)
		}
		shadows, err := installobserve.ObserveActorGuidanceShadows(canonical, fresh, repeat, cwd)
		if err != nil || !shadows.Clean() {
			return errors.Join(ErrFailed, err)
		}
		return nil
	}
	if len(operations) == 0 {
		if err := verify(); err != nil {
			return Result{}, errors.Join(ErrFailed, err)
		}
		return result, nil
	}
	var id TransactionID
	finalize := func(snapshot filetxn.Snapshot) error {
		if _, err := deriveActorGuidanceAcceptedAfter(candidate, proof, observation, snapshot); err != nil {
			return err
		}
		var err error
		id, err = transactionID(candidate, snapshot)
		return err
	}
	if _, err := apply(candidate.RootPath(), backupRoot, backupName, directoriesFor(candidate, operations), operations, verify, finalize); err != nil || !id.Valid() {
		return Result{}, errors.Join(ErrFailed, err)
	}
	result.transactionID = id
	return result, nil
}
