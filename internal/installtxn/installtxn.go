// Package installtxn materializes a bundle-bound installation candidate transactionally.
package installtxn

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/refactor-ia/cortex/internal/filetxn"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
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

type applyVerifiedTransaction func(string, string, string, []filetxn.Directory, []filetxn.Operation, func() error) (filetxn.Snapshot, error)

// ApplyVerified materializes an actor-aware candidate only after a fresh
// ownership and shadow preflight. It accepts state only after final readback.
func ApplyVerified(candidate installplan.Plan, cwd, backupRoot, backupName string) (Result, error) {
	return applyVerifiedWith(candidate, cwd, backupRoot, backupName, filetxn.ApplyOperationsWithDirectoriesAndVerify)
}

func applyVerifiedWith(candidate installplan.Plan, cwd, backupRoot, backupName string, apply applyVerifiedTransaction) (Result, error) {
	if apply == nil || candidate.InstalledState().SchemaVersion() != 2 || !validRoot(candidate.RootPath()) || !validCWD(cwd) {
		return Result{}, ErrInvalid
	}
	current, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		return Result{}, ErrFailed
	}
	classified, err := installobserve.ClassifyFilesystem(candidate, current)
	if err != nil || !verifiedDecisionsReady(classified) {
		return Result{}, ErrConflict
	}
	shadows, err := installobserve.ObserveActorShadows(candidate, current, cwd)
	if err != nil || !shadows.Clean() {
		return Result{}, ErrConflict
	}
	operations, result, err := verifiedOperations(candidate, current, classified)
	if err != nil {
		return Result{}, ErrInvalid
	}
	verify := func() error { return verifyAccepted(candidate, cwd) }
	if len(operations) == 0 {
		if err := verify(); err != nil {
			return Result{}, ErrFailed
		}
		return result, nil
	}
	if _, err := apply(candidate.RootPath(), backupRoot, backupName, directoriesFor(candidate, operations), operations, verify); err != nil {
		return Result{}, ErrFailed
	}
	return result, nil
}

func verifiedDecisionsReady(classified installobserve.Result) bool {
	if classified.StateAction() != ownership.Create && classified.StateAction() != ownership.Replace && classified.StateAction() != ownership.Unchanged {
		return false
	}
	for _, decision := range classified.ArtifactDecisions() {
		switch decision.Action {
		case ownership.Create, ownership.Replace, ownership.Remove, ownership.Unchanged:
			if decision.ObservedOwnership != ownership.CortexOwned {
				return false
			}
		case ownership.Preserve:
			if decision.ObservedOwnership == ownership.UserOwned {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func verifiedOperations(candidate installplan.Plan, observation installobserve.FilesystemObservation, classified installobserve.Result) ([]filetxn.Operation, Result, error) {
	decisions := make(map[string]installobserve.ArtifactDecision, len(classified.ArtifactDecisions()))
	for _, decision := range classified.ArtifactDecisions() {
		decisions[decision.LogicalID] = decision
	}
	skills, actors := make([]filetxn.Operation, 0, len(decisions)), make([]filetxn.Operation, 0, len(decisions))
	for _, file := range candidate.Files()[:len(candidate.Files())-1] {
		decision, found := decisions[file.LogicalID()]
		if !found {
			return nil, Result{}, errors.New("missing candidate decision")
		}
		operation, include, ok := operationFor(ownership.Decision{LogicalID: decision.LogicalID, Action: decision.Action}, file, observation)
		if !ok {
			return nil, Result{}, errors.New("invalid candidate decision")
		}
		if include {
			switch decision.Kind {
			case installstate.KindSkill:
				skills = append(skills, operation)
			case installstate.KindPiActor:
				actors = append(actors, operation)
			default:
				return nil, Result{}, errors.New("invalid candidate decision kind")
			}
		}
		delete(decisions, file.LogicalID())
	}
	for _, decision := range classified.ArtifactDecisions() {
		if _, pending := decisions[decision.LogicalID]; !pending || decision.Action != ownership.Remove {
			continue
		}
		if decision.Kind != installstate.KindSkill {
			return nil, Result{}, errors.New("invalid removal decision kind")
		}
		operation, include, ok := operationFor(ownership.Decision{LogicalID: decision.LogicalID, Action: decision.Action}, installplan.File{}, observation)
		if !ok {
			return nil, Result{}, errors.New("invalid removal decision")
		}
		if include {
			skills = append(skills, operation)
		}
	}
	operations := append(skills, actors...)
	state, found := candidate.Files()[len(candidate.Files())-1], true
	operation, include, ok := stateOperation(classified.StateAction(), state, found, observation)
	if !ok {
		return nil, Result{}, errors.New("invalid state decision")
	}
	if include {
		operations = append(operations, operation)
	}
	return operations, Result{actions: actionsFromClassification(classified)}, nil
}

func verifyAccepted(candidate installplan.Plan, cwd string) error {
	observation, err := installobserve.Observe(candidate, installobserve.DefaultOptions())
	if err != nil {
		return err
	}
	classified, err := installobserve.ClassifyFilesystem(candidate, observation)
	if err != nil || classified.StateAction() != ownership.Unchanged {
		return errors.New("accepted state readback is invalid")
	}
	for _, decision := range classified.ArtifactDecisions() {
		if decision.ObservedOwnership != ownership.CortexOwned || decision.Action != ownership.Unchanged {
			return errors.New("accepted artifact readback is invalid")
		}
	}
	shadows, err := installobserve.ObserveActorShadows(candidate, observation, cwd)
	if err != nil || !shadows.Clean() {
		return errors.New("accepted actor shadows are invalid")
	}
	return nil
}

func actionsFromClassification(classified installobserve.Result) []Action {
	decisions := classified.ArtifactDecisions()
	actions := make([]Action, 0, len(decisions)+1)
	for _, decision := range decisions {
		actions = append(actions, Action{LogicalID: decision.LogicalID, Action: decision.Action})
	}
	return append(actions, Action{LogicalID: "state/install-state", Action: classified.StateAction()})
}

func validCWD(cwd string) bool {
	return filepath.IsAbs(cwd) && filepath.Clean(cwd) == cwd && validRoot(cwd)
}

// Apply materializes the supplied bundle-bound candidate only when its bounded
// observation matches. It writes skills before the installation state file.
func Apply(candidate installplan.Plan, observation installobserve.FilesystemObservation, backupRoot, backupName string) (Result, error) {
	if !validRoot(candidate.RootPath()) || !observation.MatchesCandidate(candidate) {
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

type GroupRequest struct {
	Plan        installplan.Plan
	Observation installobserve.FilesystemObservation
}
type Counts struct {
	Create, Replace, Remove, Unchanged, Preserve int
}
type GroupResult struct {
	runtimeIDs []runtimematrix.RuntimeID
	counts     Counts
}

func (result GroupResult) RuntimeIDs() []runtimematrix.RuntimeID {
	return append([]runtimematrix.RuntimeID(nil), result.runtimeIDs...)
}
func (result GroupResult) Counts() Counts { return result.counts }

type applyWithDirectories func(string, string, string, []filetxn.Directory, []filetxn.Operation) (filetxn.Snapshot, error)

func ApplyGroup(requests []GroupRequest, backupRoot, backupName string) (GroupResult, error) {
	return applyGroupWith(requests, backupRoot, backupName, filetxn.ApplyOperationsWithDirectories)
}

func applyGroupWith(requests []GroupRequest, backupRoot, backupName string, apply applyWithDirectories) (GroupResult, error) {
	if !validGroupRequests(requests) {
		return GroupResult{}, ErrInvalid
	}
	root, ok := transactionRoot(requests)
	if !ok {
		return GroupResult{}, ErrInvalid
	}

	result := GroupResult{runtimeIDs: make([]runtimematrix.RuntimeID, 0, len(requests))}
	skills, states := make([]filetxn.Operation, 0), make([]filetxn.Operation, 0)
	directories := make(map[string]filetxn.Directory)
	for _, request := range requests {
		current, err := installobserve.Observe(request.Plan, installobserve.DefaultOptions())
		if err != nil || !reflect.DeepEqual(request.Observation, current) {
			return GroupResult{}, ErrInvalid
		}
		operations, single, err := prepare(request.Plan, current)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return GroupResult{}, ErrConflict
			}
			return GroupResult{}, ErrInvalid
		}
		result.runtimeIDs = append(result.runtimeIDs, request.Plan.RuntimeID())
		result.counts = addCounts(result.counts, single)
		prefix, err := filepath.Rel(root, request.Plan.RootPath())
		if err != nil || prefix == "." {
			return GroupResult{}, ErrInvalid
		}
		prefix = filepath.ToSlash(prefix)
		for _, operation := range operations {
			operation, state, ok := rebaseOperation(operation, prefix)
			if !ok {
				return GroupResult{}, ErrInvalid
			}
			if state {
				states = append(states, operation)
			} else {
				skills = append(skills, operation)
			}
		}
		for _, directory := range directoriesFor(request.Plan, operations) {
			directory.Path = path.Join(prefix, directory.Path)
			directories[directory.Path] = directory
		}
	}
	operations := append(skills, states...)
	if len(operations) == 0 {
		return result, nil
	}
	directoryList := make([]filetxn.Directory, 0, len(directories))
	for _, directory := range directories {
		directoryList = append(directoryList, directory)
	}
	sort.Slice(directoryList, func(left, right int) bool {
		leftDepth, rightDepth := depth(directoryList[left].Path), depth(directoryList[right].Path)
		return leftDepth < rightDepth || leftDepth == rightDepth && directoryList[left].Path < directoryList[right].Path
	})
	if _, err := apply(root, backupRoot, backupName, directoryList, operations); err != nil {
		return GroupResult{}, ErrFailed
	}
	return result, nil
}
func validGroupRequests(requests []GroupRequest) bool {
	if len(requests) < 1 || len(requests) > 3 {
		return false
	}
	roots := make([]string, 0, len(requests))
	last, snapshot := -1, ""
	for _, request := range requests {
		plan, observation := request.Plan, request.Observation
		rank := runtimeRank(plan.RuntimeID())
		bundle, bound := plan.Bundle()
		if rank < 0 || rank <= last || !validRoot(plan.RootPath()) || !bound || !observation.MatchesCandidate(plan) || bundle.Manifest().RuntimeID() != plan.RuntimeID() || bundle.Manifest().SnapshotFingerprint() != plan.SnapshotFingerprint() {
			return false
		}
		if snapshot == "" {
			snapshot = plan.SnapshotFingerprint()
		} else if snapshot != plan.SnapshotFingerprint() {
			return false
		}
		for _, root := range roots {
			if rootsOverlap(root, plan.RootPath()) {
				return false
			}
		}
		roots, last = append(roots, plan.RootPath()), rank
	}
	return true
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
	volume := filepath.VolumeName(requests[0].Plan.RootPath())
	root := filepath.Dir(requests[0].Plan.RootPath())
	for _, request := range requests {
		if filepath.VolumeName(request.Plan.RootPath()) != volume {
			return "", false
		}
		for !containsRoot(root, request.Plan.RootPath()) {
			parent := filepath.Dir(root)
			if parent == root {
				return "", false
			}
			root = parent
		}
	}
	if !validRoot(root) {
		return "", false
	}
	return root, true
}

func rebaseOperation(operation filetxn.Operation, prefix string) (filetxn.Operation, bool, bool) {
	statePath := path.Join(prefix, ".cortex/install-state.json")
	if operation.Create != nil {
		copy := *operation.Create
		copy.Path = path.Join(prefix, copy.Path)
		return filetxn.Operation{Create: &copy}, copy.Path == statePath, true
	}
	if operation.Replace != nil {
		copy := *operation.Replace
		copy.Path = path.Join(prefix, copy.Path)
		return filetxn.Operation{Replace: &copy}, copy.Path == statePath, true
	}
	if operation.Remove != nil {
		copy := *operation.Remove
		copy.Path = path.Join(prefix, copy.Path)
		return filetxn.Operation{Remove: &copy}, copy.Path == statePath, true
	}
	return filetxn.Operation{}, false, false
}

func addCounts(counts Counts, result Result) Counts {
	for _, action := range result.Actions() {
		switch action.Action {
		case ownership.Create:
			counts.Create++
		case ownership.Replace:
			counts.Replace++
		case ownership.Remove:
			counts.Remove++
		case ownership.Unchanged:
			counts.Unchanged++
		case ownership.Preserve:
			counts.Preserve++
		}
	}
	return counts
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
