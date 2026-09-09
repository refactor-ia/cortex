// Package uninstalltxn removes a verified Cortex installation transactionally.
package uninstalltxn

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
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

// GroupRequest binds one canonical runtime root to its uninstall evidence.
type GroupRequest struct {
	RuntimeID   runtimematrix.RuntimeID
	Root        string
	Observation installobserve.UninstallObservation
}

type applyOperations func(string, string, string, []filetxn.Operation) (filetxn.Snapshot, error)

// ApplyGroup removes all requested runtime installations in one rollback transaction.
func ApplyGroup(requests []GroupRequest, backupRoot, backupName string) error {
	return applyGroupWith(requests, backupRoot, backupName, filetxn.ApplyOperations)
}

func applyGroupWith(requests []GroupRequest, backupRoot, backupName string, apply applyOperations) error {
	if err := validateGroupRequests(requests); err != nil {
		return err
	}
	root, ok := transactionRoot(requests)
	if !ok {
		return ErrInvalid
	}

	skills, states := make([]filetxn.Operation, 0), make([]filetxn.Operation, 0)
	for _, request := range requests {
		operations, err := operationsFor(request.Observation)
		if err != nil {
			return err
		}
		prefix, err := filepath.Rel(root, request.Root)
		if err != nil || prefix == "." {
			return ErrInvalid
		}
		prefix = filepath.ToSlash(prefix)
		for _, operation := range operations {
			if operation.Remove == nil {
				return ErrInvalid
			}
			remove := *operation.Remove
			state := remove.Path == ".cortex/install-state.json"
			remove.Path = path.Join(prefix, remove.Path)
			operation = filetxn.Operation{Remove: &remove}
			if state {
				states = append(states, operation)
			} else {
				skills = append(skills, operation)
			}
		}
	}
	operations := append(skills, states...)
	if len(operations) == 0 {
		return nil
	}
	if _, err := apply(root, backupRoot, backupName, operations); err != nil {
		return ErrFailed
	}
	return nil
}

func validateGroupRequests(requests []GroupRequest) error {
	if len(requests) < 1 || len(requests) > 3 {
		return ErrInvalid
	}
	last := -1
	for index, request := range requests {
		rank := runtimeRank(request.RuntimeID)
		if rank < 0 || rank <= last || !validRoot(request.Root) {
			return ErrInvalid
		}
		if !request.Observation.MatchesRoot(request.Root) || !request.Observation.MatchesRuntime(request.RuntimeID) || !request.Observation.Ready() {
			return ErrConflict
		}
		for prior := 0; prior < index; prior++ {
			if rootsOverlap(requests[prior].Root, request.Root) {
				return ErrInvalid
			}
		}
		last = rank
	}
	return nil
}

func runtimeRank(id runtimematrix.RuntimeID) int {
	for index, canonical := range []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode} {
		if id == canonical {
			return index
		}
	}
	return -1
}

func rootsOverlap(left, right string) bool {
	return containsRoot(left, right) || containsRoot(right, left)
}

func containsRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && (relative == "." || relative != ".." && !(len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator)))
}

func transactionRoot(requests []GroupRequest) (string, bool) {
	volume := filepath.VolumeName(requests[0].Root)
	root := filepath.Dir(requests[0].Root)
	for _, request := range requests {
		if filepath.VolumeName(request.Root) != volume {
			return "", false
		}
		for !containsRoot(root, request.Root) {
			parent := filepath.Dir(root)
			if parent == root {
				return "", false
			}
			root = parent
		}
	}
	return root, validRoot(root)
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
