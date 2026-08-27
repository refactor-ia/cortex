// Package uninstalltxn removes a verified Cortex installation transactionally.
package uninstalltxn

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
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

func validRoot(root string) bool {
	info, err := os.Lstat(root)
	return err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0
}
