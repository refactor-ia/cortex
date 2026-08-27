// Package installtxn materializes a bundle-bound installation candidate transactionally.
package installtxn

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
)

var (
	ErrInvalid  = errors.New("install transaction: invalid input")
	ErrConflict = errors.New("install transaction: ownership conflict")
	ErrFailed   = errors.New("install transaction: failed")
)

// Action is bounded neutral evidence for one logical artifact decision.
type Action struct {
	LogicalID string
	Action    ownership.Action
}

// Result contains detached neutral evidence and never includes filesystem paths.
type Result struct{ actions []Action }

// Actions returns the canonical logical artifact actions.
func (result Result) Actions() []Action { return append([]Action(nil), result.actions...) }

// Apply materializes the supplied bundle-bound candidate only when its bounded
// observation matches. It writes skills before the installation state file.
func Apply(candidate installplan.Plan, observation installobserve.FilesystemObservation, backupRoot, backupName string) (Result, error) {
	if !validRoot(candidate.RootPath()) {
		return Result{}, ErrInvalid
	}
	current, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil || !reflect.DeepEqual(observation, current) {
		return Result{}, ErrInvalid
	}
	operations, result, err := prepare(candidate, current)
	if err != nil || len(operations) == 0 {
		return result, err
	}
	if _, err := filetxn.ApplyOperationsWithDirectories(candidate.RootPath(), backupRoot, backupName, directoriesFor(candidate, operations), operations); err != nil {
		return Result{}, ErrFailed
	}
	return result, nil
}

func operationsFor(candidate installplan.Plan, observation installobserve.FilesystemObservation) ([]filetxn.Operation, error) {
	operations, _, err := prepare(candidate, observation)
	return operations, err
}

func prepare(candidate installplan.Plan, observation installobserve.FilesystemObservation) ([]filetxn.Operation, Result, error) {
	bundle, bound := candidate.Bundle()
	if !bound {
		return nil, Result{}, ErrInvalid
	}
	classified, err := installobserve.Classify(candidate, observation.PriorState(), observation.Slots())
	if err != nil {
		return nil, Result{}, ErrInvalid
	}
	plan, err := ownership.Build(bundle, classified.Observed())
	if err != nil {
		return nil, Result{}, ErrInvalid
	}
	result := Result{actions: actions(plan.Decisions(), classified.StateAction())}
	if !plan.Ready() {
		return nil, result, ErrConflict
	}
	files := make(map[string]installplan.File, len(candidate.Files()))
	for _, file := range candidate.Files() {
		files[file.LogicalID()] = file
	}
	operations := make([]filetxn.Operation, 0, len(plan.Decisions())+1)
	for _, decision := range plan.Decisions() {
		operation, include, ok := operationFor(decision, files[decision.LogicalID], observation)
		if !ok {
			return nil, Result{}, ErrInvalid
		}
		if include {
			operations = append(operations, operation)
		}
	}
	state, found := files["state/install-state"]
	operation, include, ok := stateOperation(classified.StateAction(), state, found, observation)
	if !ok {
		return nil, Result{}, ErrInvalid
	}
	if include {
		operations = append(operations, operation)
	}
	return operations, result, nil
}

func operationFor(decision ownership.Decision, file installplan.File, observation installobserve.FilesystemObservation) (filetxn.Operation, bool, bool) {
	switch decision.Action {
	case ownership.Unchanged, ownership.Preserve:
		return filetxn.Operation{}, false, true
	case ownership.Create:
		return filetxn.Operation{Create: &filetxn.Create{Path: file.RelativePath(), Data: file.Content(), Mode: file.DesiredMode()}}, true, file.LogicalID() == decision.LogicalID && !hasExact(observation, decision.LogicalID)
	case ownership.Replace:
		exact, found := observation.Exact(decision.LogicalID)
		return filetxn.Operation{Replace: &filetxn.Replace{Path: file.RelativePath(), ExpectedData: exact.Bytes(), ExpectedMode: exact.Mode(), Data: file.Content(), Mode: file.DesiredMode()}}, true, found && file.LogicalID() == decision.LogicalID
	case ownership.Remove:
		exact, found := observation.Exact(decision.LogicalID)
		return filetxn.Operation{Remove: &filetxn.Remove{Path: priorPath(decision.LogicalID, observation), ExpectedData: exact.Bytes(), ExpectedMode: exact.Mode()}}, true, found
	}
	return filetxn.Operation{}, false, false
}

func stateOperation(action ownership.Action, file installplan.File, found bool, observation installobserve.FilesystemObservation) (filetxn.Operation, bool, bool) {
	if !found || file.Role() != "state" || file.RelativePath() != ".cortex/install-state.json" || file.DesiredMode() != installplan.CanonicalFileMode {
		return filetxn.Operation{}, false, false
	}
	switch action {
	case ownership.Unchanged:
		return filetxn.Operation{}, false, true
	case ownership.Create:
		return filetxn.Operation{Create: &filetxn.Create{Path: file.RelativePath(), Data: file.Content(), Mode: file.DesiredMode()}}, true, !hasExact(observation, file.LogicalID())
	case ownership.Replace:
		exact, exactFound := observation.Exact(file.LogicalID())
		return filetxn.Operation{Replace: &filetxn.Replace{Path: file.RelativePath(), ExpectedData: exact.Bytes(), ExpectedMode: exact.Mode(), Data: file.Content(), Mode: file.DesiredMode()}}, true, exactFound
	}
	return filetxn.Operation{}, false, false
}

func priorPath(id string, observation installobserve.FilesystemObservation) string {
	prior := observation.PriorState()
	if prior == nil {
		return ""
	}
	for _, artifact := range prior.Manifest.Artifacts() {
		if artifact.LogicalID() == id {
			return artifact.RelativePath()
		}
	}
	return ""
}
func hasExact(observation installobserve.FilesystemObservation, id string) bool {
	_, found := observation.Exact(id)
	return found
}
func actions(decisions []ownership.Decision, state ownership.Action) []Action {
	out := make([]Action, 0, len(decisions)+1)
	for _, decision := range decisions {
		out = append(out, Action{LogicalID: decision.LogicalID, Action: decision.Action})
	}
	return append(out, Action{LogicalID: "state/install-state", Action: state})
}
func directoriesFor(candidate installplan.Plan, operations []filetxn.Operation) []filetxn.Directory {
	files := map[string]installplan.File{}
	for _, file := range candidate.Files() {
		files[file.RelativePath()] = file
	}
	paths := map[string]bool{}
	for _, operation := range operations {
		var relative string
		if operation.Create != nil {
			relative = operation.Create.Path
		} else if operation.Replace != nil {
			relative = operation.Replace.Path
		} else {
			continue
		}
		if _, found := files[relative]; !found {
			continue
		}
		for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
			paths[parent] = true
		}
	}
	directories := make([]filetxn.Directory, 0, len(paths))
	for directory := range paths {
		directories = append(directories, filetxn.Directory{Path: directory, Mode: 0o700})
	}
	sort.Slice(directories, func(left, right int) bool {
		leftDepth, rightDepth := depth(directories[left].Path), depth(directories[right].Path)
		return leftDepth < rightDepth || leftDepth == rightDepth && directories[left].Path < directories[right].Path
	})
	return directories
}
func depth(value string) int {
	depth := 1
	for _, character := range value {
		if character == '/' {
			depth++
		}
	}
	return depth
}
func validRoot(root string) bool {
	info, err := os.Lstat(root)
	return err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0
}
