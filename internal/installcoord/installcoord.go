// Package installcoord builds read-only, cross-runtime install preflight reports.
package installcoord

import (
	"bytes"
	"errors"

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

var ErrInvalid = errors.New("install coordinator: invalid input")

// Unit is one exact candidate and its matching filesystem observation; it carries no mutation authority.
type Unit struct {
	Plan        installplan.Plan
	Observation installobserve.FilesystemObservation
}

type Action = ownership.Action

const (
	ActionCreate    = ownership.Create
	ActionReplace   = ownership.Replace
	ActionRemove    = ownership.Remove
	ActionUnchanged = ownership.Unchanged
	ActionPreserve  = ownership.Preserve
	ActionConflict  = ownership.Conflict
)

type Evidence struct {
	Create, Replace, Remove, Unchanged, Preserve, Conflict int
}

// Status is one canonical runtime result without candidate or filesystem data.
type Status struct {
	RuntimeID runtimematrix.RuntimeID
	Outcome   runtimematrix.Outcome
	Action    runtimematrix.Action
	actions   []Action
	ready     bool
}

func (status Status) Actions() []Action { return append([]Action{}, status.actions...) }

func (status Status) Evidence() Evidence {
	var evidence Evidence
	for _, action := range status.actions {
		switch action {
		case ActionCreate:
			evidence.Create++
		case ActionReplace:
			evidence.Replace++
		case ActionRemove:
			evidence.Remove++
		case ActionUnchanged:
			evidence.Unchanged++
		case ActionPreserve:
			evidence.Preserve++
		case ActionConflict:
			evidence.Conflict++
		}
	}
	return evidence
}

// Ready reports conflict-free evidence only, never authority to mutate a root.
func (status Status) Ready() bool { return status.ready }

// Report is immutable read-only evidence and retains no plans or observations.
type Report struct {
	statuses []Status
	ready    bool
}

func (report Report) Statuses() []Status {
	statuses := make([]Status, len(report.statuses))
	for index, status := range report.statuses {
		statuses[index] = status
		statuses[index].actions = status.Actions()
	}
	return statuses
}

// Ready reports clean evidence only; a transaction must separately revalidate.
func (report Report) Ready() bool { return report.ready }

func Preflight(observations []runtimematrix.Observation, units []Unit) (Report, error) {
	matrix, err := runtimematrix.Decide(observations)
	if err != nil {
		return Report{}, ErrInvalid
	}
	return preflight(matrix, units)
}

// PreflightLocalUpdate preserves strict preflight evidence except for one
// explicitly selected local target derived by local planning.
func PreflightLocalUpdate(observations []runtimematrix.Observation, target runtimematrix.RuntimeID, units []Unit) (Report, error) {
	matrix, err := runtimematrix.DecideLocalUpdate(observations, target)
	if err != nil {
		return Report{}, ErrInvalid
	}
	return preflight(matrix, units)
}

func preflight(matrix runtimematrix.Matrix, units []Unit) (Report, error) {
	byRuntime := make(map[runtimematrix.RuntimeID]Unit, len(units))
	for _, unit := range units {
		id := unit.Plan.RuntimeID()
		if !validUnit(unit) {
			return Report{}, ErrInvalid
		}
		if _, duplicate := byRuntime[id]; duplicate {
			return Report{}, ErrInvalid
		}
		byRuntime[id] = unit
	}

	statuses := make([]Status, 0, len(matrix.Decisions))
	var snapshot string
	ready, participants := true, 0
	for _, decision := range matrix.Decisions {
		status := Status{RuntimeID: decision.ID, Outcome: decision.Outcome, Action: decision.Action}
		unit, supplied := byRuntime[decision.ID]
		if !decision.IncludeInTransaction {
			if supplied {
				return Report{}, ErrInvalid
			}
			statuses = append(statuses, status)
			continue
		}
		if !supplied || unit.Plan.RuntimeID() != decision.ID {
			return Report{}, ErrInvalid
		}
		if snapshot == "" {
			snapshot = unit.Plan.SnapshotFingerprint()
		} else if snapshot != unit.Plan.SnapshotFingerprint() {
			return Report{}, ErrInvalid
		}
		classified, err := Classify(unit.Plan, unit.Observation)
		if err != nil {
			return Report{}, ErrInvalid
		}
		bundle, _ := unit.Plan.Bundle()
		ownershipPlan, err := ownership.Build(bundle, classified.Observed())
		if err != nil {
			return Report{}, ErrInvalid
		}
		status.actions = actions(ownershipPlan.Decisions(), classified.StateAction())
		status.ready = ownershipPlan.Ready()
		if unit.Plan.InstalledState().SchemaVersion() == 2 {
			status.actions = classifiedActions(classified)
			status.ready = classifiedReady(classified)
		}
		ready = ready && status.ready
		participants++
		statuses = append(statuses, status)
	}
	if len(byRuntime) != participants {
		return Report{}, ErrInvalid
	}
	ready = ready && participants > 0
	if !ready {
		for index := range statuses {
			statuses[index].ready = false
		}
	}
	return Report{statuses: statuses, ready: ready}, nil
}

// Classify dispatches the canonical classification path for one bound candidate:
// the v1 in-memory API for skill-only plans and the filesystem API for
// actor-aware v2 plans. The observation must already match the candidate.
func Classify(plan installplan.Plan, observation installobserve.FilesystemObservation) (installobserve.Result, error) {
	if plan.InstalledState().SchemaVersion() == 1 {
		return installobserve.Classify(plan, observation.PriorState(), observation.Slots())
	}
	return installobserve.ClassifyFilesystem(plan, observation)
}

// classifiedActions reports every classified artifact decision plus the state action.
func classifiedActions(classified installobserve.Result) []Action {
	decisions := classified.ArtifactDecisions()
	actions := make([]Action, 0, len(decisions)+1)
	for _, decision := range decisions {
		actions = append(actions, decision.Action)
	}
	return append(actions, classified.StateAction())
}

// classifiedReady reports conflict-free v2 evidence: the state action must be
// materializable and no classified artifact decision may conflict.
func classifiedReady(classified installobserve.Result) bool {
	state := classified.StateAction()
	if state != ownership.Create && state != ownership.Replace && state != ownership.Unchanged {
		return false
	}
	for _, decision := range classified.ArtifactDecisions() {
		if decision.Action == ownership.Conflict {
			return false
		}
	}
	return true
}

func validUnit(unit Unit) bool {
	bundle, bound := unit.Plan.Bundle()
	if !bound || !unit.Observation.MatchesCandidate(unit.Plan) || bundle.Manifest().RuntimeID() != unit.Plan.RuntimeID() || bundle.Manifest().SnapshotFingerprint() != unit.Plan.SnapshotFingerprint() {
		return false
	}
	artifacts, files := bundle.Artifacts(), unit.Plan.Files()
	if unit.Plan.InstalledState().SchemaVersion() == 2 {
		// Actor-aware v2 candidates carry actor files beyond the bound skill
		// bundle; structural actor, state, path, and mode checks are already
		// enforced by the observation binding.
		if len(files) < len(artifacts)+2 {
			return false
		}
	} else if len(files) != len(artifacts)+1 {
		return false
	}
	for index, artifact := range artifacts {
		file := files[index]
		if file.Role() != "skill" || file.LogicalID() != artifact.LogicalID() || file.SHA256() != artifact.SHA256() || !bytes.Equal(file.Content(), artifact.Content()) {
			return false
		}
	}
	return true
}

func actions(decisions []ownership.Decision, state ownership.Action) []Action {
	actions := make([]Action, 0, len(decisions)+1)
	for _, decision := range decisions {
		actions = append(actions, decision.Action)
	}
	return append(actions, state)
}
