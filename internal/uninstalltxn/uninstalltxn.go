// Package uninstalltxn removes a verified Cortex installation transactionally.
package uninstalltxn

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
)

var (
	ErrInvalid  = errors.New("uninstall transaction: invalid input")
	ErrConflict = errors.New("uninstall transaction: ownership conflict")
	ErrFailed   = errors.New("uninstall transaction: failed")
)

// Action describes a path-neutral uninstall decision for one logical artifact.
type Action string

const (
	ActionAbsent   Action = "absent"
	ActionRemove   Action = "remove"
	ActionConflict Action = "conflict"
)

// Result contains detached logical decisions and no filesystem locations.
type Result struct{ actions []Decision }

// Decision is the neutral uninstall action for one Cortex logical artifact.
type Decision struct {
	LogicalID string
	Action    Action
}

// Actions returns a detached copy in canonical prior-state order.
func (result Result) Actions() []Decision { return append([]Decision(nil), result.actions...) }

// Apply removes only the exact candidates authorized by observation. It preserves the
// observation order, so skills are removed before the canonical state file.
func Apply(root string, observation installobserve.UninstallObservation, backupRoot, backupName string) (Result, error) {
	if !observation.MatchesRoot(root) {
		return Result{}, ErrConflict
	}
	if !validRoot(root) {
		return Result{}, ErrInvalid
	}
	result := Result{actions: actions(observation.Records())}
	if !observation.Ready() {
		return result, ErrConflict
	}
	operations, err := operationsFor(observation)
	if err != nil {
		return Result{}, err
	}
	if len(operations) == 0 {
		return result, nil
	}
	if _, err := filetxn.ApplyOperations(root, backupRoot, backupName, operations); err != nil {
		return Result{}, ErrFailed
	}
	return result, nil
}

// ApplyVerified removes an exact actor-aware v2 candidate only after a fresh
// filesystem classification and shadow scan prove every owned asset unchanged.
func ApplyVerified(candidate installplan.Plan, cwd, backupRoot, backupName string) (Result, error) {
	if candidate.InstalledState().SchemaVersion() != 2 || !canonicalRoot(candidate.RootPath()) || !canonicalRoot(cwd) {
		return Result{}, ErrInvalid
	}
	observation, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		return Result{}, ErrFailed
	}
	classified, err := installobserve.ClassifyFilesystem(candidate, observation)
	if err != nil || !verifiedUninstallReady(candidate, classified) {
		return Result{}, ErrConflict
	}
	shadows, err := installobserve.ObserveActorShadows(candidate, observation, cwd)
	if err != nil || !shadows.Clean() {
		return Result{}, ErrConflict
	}
	operations, result, err := verifiedUninstallOperations(candidate, observation)
	if err != nil {
		return Result{}, ErrInvalid
	}
	if _, err := filetxn.ApplyOperations(candidate.RootPath(), backupRoot, backupName, operations); err != nil {
		return Result{}, ErrFailed
	}
	return result, nil
}

func verifiedUninstallReady(candidate installplan.Plan, classified installobserve.Result) bool {
	if classified.StateAction() != ownership.Unchanged {
		return false
	}
	decisions := classified.ArtifactDecisions()
	files := candidate.Files()
	if len(files) < 2 || len(decisions) != len(files)-1 {
		return false
	}
	byID := make(map[string]installobserve.ArtifactDecision, len(decisions))
	for _, decision := range decisions {
		if _, exists := byID[decision.LogicalID]; exists {
			return false
		}
		byID[decision.LogicalID] = decision
	}
	for _, file := range files[:len(files)-1] {
		decision, found := byID[file.LogicalID()]
		kind := installstate.KindSkill
		if file.Role() == "actor" {
			kind = installstate.KindPiActor
		}
		if !found || (file.Role() != "skill" && file.Role() != "actor") || decision.Kind != kind ||
			decision.ObservedOwnership != ownership.CortexOwned || decision.Action != ownership.Unchanged {
			return false
		}
	}
	return files[len(files)-1].Role() == "state" && files[len(files)-1].LogicalID() == "state/install-state"
}

func verifiedUninstallOperations(candidate installplan.Plan, observation installobserve.FilesystemObservation) ([]filetxn.Operation, Result, error) {
	files := candidate.Files()
	operations := make([]filetxn.Operation, 0, len(files))
	decisions := make([]Decision, 0, len(files))
	for index, file := range files {
		if (index == len(files)-1) != (file.Role() == "state") {
			return nil, Result{}, ErrInvalid
		}
		exact, found := observation.Exact(file.LogicalID())
		if !found {
			return nil, Result{}, ErrInvalid
		}
		operations = append(operations, filetxn.Operation{Remove: &filetxn.Remove{
			Path: file.RelativePath(), ExpectedData: exact.Bytes(), ExpectedMode: exact.Mode(),
		}})
		decisions = append(decisions, Decision{LogicalID: file.LogicalID(), Action: ActionRemove})
	}
	return operations, Result{actions: decisions}, nil
}

func operationsFor(observation installobserve.UninstallObservation) ([]filetxn.Operation, error) {
	if !observation.Ready() {
		return nil, ErrConflict
	}
	candidates := observation.RemovalCandidates()
	operations := make([]filetxn.Operation, 0, len(candidates))
	for index, candidate := range candidates {
		if candidate.Status != installobserve.UninstallRemove || !validCandidate(candidate.LogicalID, index == len(candidates)-1) {
			return nil, ErrInvalid
		}
		evidence, found := observation.RemovalEvidence(candidate.LogicalID)
		if !found {
			return nil, ErrInvalid
		}
		operations = append(operations, filetxn.Operation{Remove: &filetxn.Remove{
			Path: evidence.Destination(), ExpectedData: evidence.Bytes(), ExpectedMode: evidence.Mode(),
		}})
	}
	return operations, nil
}

func actions(records []installobserve.UninstallRecord) []Decision {
	out := make([]Decision, 0, len(records))
	for _, record := range records {
		switch record.Status {
		case installobserve.UninstallAbsent:
			out = append(out, Decision{LogicalID: record.LogicalID, Action: ActionAbsent})
		case installobserve.UninstallRemove:
			out = append(out, Decision{LogicalID: record.LogicalID, Action: ActionRemove})
		case installobserve.UninstallConflict:
			out = append(out, Decision{LogicalID: record.LogicalID, Action: ActionConflict})
		}
	}
	return out
}

func validCandidate(logicalID string, final bool) bool {
	if logicalID == "state/install-state" {
		return final
	}
	return !final && strings.HasPrefix(logicalID, "skills/")
}

func canonicalRoot(root string) bool {
	if !validRoot(root) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(root)
	return err == nil && resolved == root
}

func validRoot(root string) bool {
	info, err := os.Lstat(root)
	return err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0
}
