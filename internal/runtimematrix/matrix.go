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
	OutcomePresentCompatible Outcome = "present_compatible"
	OutcomeAbsent            Outcome = "absent"
	OutcomeKnownIncompatible Outcome = "known_incompatible"
	OutcomeUnknownVersion    Outcome = "unknown_version"
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
		if observation.Version != "" {
			return errors.New("known version requires adapter compatibility")
		}
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
