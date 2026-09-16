// Package runtimematrix decides which observed runtimes may join a transaction.
package runtimematrix

import "errors"

// RuntimeID identifies a supported runtime.
type RuntimeID string

const (
	RuntimePi         RuntimeID = "pi"
	RuntimeOpenCode   RuntimeID = "opencode"
	RuntimeClaudeCode RuntimeID = "claude-code"
)

var runtimeOrder = []RuntimeID{RuntimePi, RuntimeOpenCode, RuntimeClaudeCode}

// Compatibility records an adapter's known compatibility with an observed version.
type Compatibility string

const (
	CompatibilityUnknown Compatibility = "unknown"
	Compatible           Compatibility = "compatible"
	Incompatible         Compatibility = "incompatible"
)

// Action is the non-mutating recommendation for a runtime.
type Action string

const (
	Configure Action = "configure"
	Warn      Action = "warn"
	Skip      Action = "skip"
)

// Outcome is the stable machine-readable reason for a runtime decision.
type Outcome string

const (
	OutcomePresentCompatible  Outcome = "present_compatible"
	OutcomePresentUncertified Outcome = "present_uncertified"
	OutcomeAbsent             Outcome = "absent"
	OutcomeKnownIncompatible  Outcome = "known_incompatible"
	OutcomeUnknownVersion     Outcome = "unknown_version"
)

// Observation is a future adapter's report about one supported runtime.
// A known version is any non-empty string; this package does not parse it.
type Observation struct {
	ID            RuntimeID
	Present       bool
	Version       string
	Compatibility Compatibility
}

// Decision states whether a runtime is eligible for a later transaction.
type Decision struct {
	ID                   RuntimeID
	Outcome              Outcome
	Action               Action
	IncludeInTransaction bool
	TouchAllowed         bool
}

// Matrix contains one decision for each supported runtime.
type Matrix struct {
	Decisions     []Decision
	HasCompatible bool
}

// Decide validates observations and returns decisions in supported runtime order.
func Decide(observations []Observation) (Matrix, error) {
	byID := make(map[RuntimeID]Observation, len(runtimeOrder))
	for _, observation := range observations {
		if !isSupported(observation.ID) {
			return Matrix{}, errors.New("unknown runtime observation")
		}
		if _, exists := byID[observation.ID]; exists {
			return Matrix{}, errors.New("duplicate runtime observation")
		}
		if err := validate(observation); err != nil {
			return Matrix{}, err
		}
		byID[observation.ID] = observation
	}

	matrix := Matrix{Decisions: make([]Decision, 0, len(runtimeOrder))}
	for _, id := range runtimeOrder {
		observation, exists := byID[id]
		if !exists {
			return Matrix{}, errors.New("missing runtime observation")
		}

		decision := decisionFor(observation)
		matrix.Decisions = append(matrix.Decisions, decision)
		matrix.HasCompatible = matrix.HasCompatible || decision.IncludeInTransaction
	}
	return matrix, nil
}

// DecideLocalUpdate narrows strict decisions to exactly one selected runtime.
// A present normalized uncertified version is eligible only for this explicit
// local update; strict Decide never emits or authorizes this outcome.
func DecideLocalUpdate(observations []Observation, target RuntimeID) (Matrix, error) {
	matrix, err := Decide(observations)
	if err != nil || !isSupported(target) {
		return Matrix{}, errors.New("invalid local update input")
	}
	for index := range matrix.Decisions {
		decision := &matrix.Decisions[index]
		if decision.ID != target {
			decision.IncludeInTransaction = false
			decision.TouchAllowed = false
			continue
		}
		for _, observation := range observations {
			if observation.ID == target && observation.Present && observation.Version != "" && observation.Compatibility == CompatibilityUnknown {
				decision.Outcome = OutcomePresentUncertified
				decision.Action = Configure
				decision.IncludeInTransaction = true
				decision.TouchAllowed = true
			}
		}
	}
	matrix.HasCompatible = false
	for _, decision := range matrix.Decisions {
		matrix.HasCompatible = matrix.HasCompatible || decision.IncludeInTransaction
	}
	return matrix, nil
}

func isSupported(id RuntimeID) bool {
	for _, supported := range runtimeOrder {
		if id == supported {
			return true
		}
	}
	return false
}

func validate(observation Observation) error {
	if !observation.Present {
		if observation.Version != "" {
			return errors.New("absent runtime cannot have a version")
		}
		if observation.Compatibility != CompatibilityUnknown {
			return errors.New("absent runtime cannot have adapter compatibility")
		}
		return nil
	}

	switch observation.Compatibility {
	case CompatibilityUnknown:
		// A normalized observed version may remain uncertified.
	case Compatible, Incompatible:
		if observation.Version == "" {
			return errors.New("unknown version cannot have adapter compatibility")
		}
	default:
		return errors.New("unknown adapter compatibility")
	}
	return nil
}

func decisionFor(observation Observation) Decision {
	if !observation.Present {
		return Decision{ID: observation.ID, Outcome: OutcomeAbsent, Action: Warn}
	}
	if observation.Compatibility == Compatible {
		return Decision{
			ID:                   observation.ID,
			Outcome:              OutcomePresentCompatible,
			Action:               Configure,
			IncludeInTransaction: true,
			TouchAllowed:         true,
		}
	}
	if observation.Compatibility == Incompatible {
		return Decision{ID: observation.ID, Outcome: OutcomeKnownIncompatible, Action: Skip}
	}
	return Decision{ID: observation.ID, Outcome: OutcomeUnknownVersion, Action: Warn}
}
