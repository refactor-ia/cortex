// Package runtimecompat classifies bounded runtime probes by exact certified versions.
package runtimecompat

import (
	"errors"
	"regexp"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
)

var runtimeOrder = [3]runtimematrix.RuntimeID{
	runtimematrix.RuntimePi,
	runtimematrix.RuntimeOpenCode,
	runtimematrix.RuntimeClaudeCode,
}

var normalizedVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$`)

// Entry is the explicit certification policy for one runtime.
// Each version must be a normalized semantic version and matches only exactly.
type Entry struct {
	ID                  runtimematrix.RuntimeID
	CertifiedCompatible []string
	KnownIncompatible   []string
}

type entry struct {
	compatible   map[string]struct{}
	incompatible map[string]struct{}
}

// Policy is an immutable exact-version compatibility policy.
type Policy struct {
	entries [3]entry
}

// NewPolicy validates and detaches an explicit policy from its mutable input.
func NewPolicy(entries []Entry) (Policy, error) {
	if len(entries) != len(runtimeOrder) {
		return Policy{}, errors.New("missing runtime policy entry")
	}
	policy := Policy{}
	seen := make(map[runtimematrix.RuntimeID]bool, len(runtimeOrder))
	for _, source := range entries {
		index, ok := runtimeIndex(source.ID)
		if !ok {
			return Policy{}, errors.New("unknown runtime policy entry")
		}
		if seen[source.ID] {
			return Policy{}, errors.New("duplicate runtime policy entry")
		}
		seen[source.ID] = true
		current, err := newEntry(source)
		if err != nil {
			return Policy{}, err
		}
		policy.entries[index] = current
	}
	for _, id := range runtimeOrder {
		if !seen[id] {
			return Policy{}, errors.New("missing runtime policy entry")
		}
	}
	return policy, nil
}

func newEntry(source Entry) (entry, error) {
	current := entry{compatible: make(map[string]struct{}, len(source.CertifiedCompatible)), incompatible: make(map[string]struct{}, len(source.KnownIncompatible))}
	for _, version := range source.CertifiedCompatible {
		if !validVersion(version) {
			return entry{}, errors.New("invalid policy version")
		}
		if _, exists := current.compatible[version]; exists {
			return entry{}, errors.New("duplicate policy version")
		}
		current.compatible[version] = struct{}{}
	}
	for _, version := range source.KnownIncompatible {
		if !validVersion(version) {
			return entry{}, errors.New("invalid policy version")
		}
		if _, exists := current.incompatible[version]; exists {
			return entry{}, errors.New("duplicate policy version")
		}
		if _, overlaps := current.compatible[version]; overlaps {
			return entry{}, errors.New("overlapping policy version")
		}
		current.incompatible[version] = struct{}{}
	}
	return current, nil
}

// BuiltInPolicy returns the production policy. Certification requires an explicit
// source and test change after real runtime smoke evidence is merged.
func BuiltInPolicy() Policy {
	policy, err := NewPolicy([]Entry{
		{ID: runtimematrix.RuntimePi},
		{ID: runtimematrix.RuntimeOpenCode},
		{ID: runtimematrix.RuntimeClaudeCode},
	})
	if err != nil {
		panic(err)
	}
	return policy
}

// Evaluate converts canonical probe reports to canonical matrix observations.
func (p Policy) Evaluate(reports []runtimeprobe.Report) ([]runtimematrix.Observation, error) {
	if !p.valid() || len(reports) != len(runtimeOrder) {
		return nil, errors.New("invalid runtime compatibility input")
	}
	observations := make([]runtimematrix.Observation, len(runtimeOrder))
	for index, report := range reports {
		if report.RuntimeID() != runtimeOrder[index] {
			return nil, errors.New("invalid runtime compatibility input")
		}
		observation, err := p.evaluateReport(index, report)
		if err != nil {
			return nil, err
		}
		observations[index] = observation
	}
	if _, err := runtimematrix.Decide(observations); err != nil {
		return nil, errors.New("invalid runtime compatibility input")
	}
	return observations, nil
}

func (p Policy) evaluateReport(index int, report runtimeprobe.Report) (runtimematrix.Observation, error) {
	observation := runtimematrix.Observation{ID: runtimeOrder[index], Compatibility: runtimematrix.CompatibilityUnknown}
	switch report.Status() {
	case runtimeprobe.Absent:
		if report.DetectedVersion() != "" {
			return runtimematrix.Observation{}, errors.New("invalid runtime compatibility input")
		}
		return observation, nil
	case runtimeprobe.VersionDetected:
		version := report.DetectedVersion()
		if !validVersion(version) {
			return runtimematrix.Observation{}, errors.New("invalid runtime compatibility input")
		}
		observation.Present = true
		if _, ok := p.entries[index].compatible[version]; ok {
			observation.Version, observation.Compatibility = version, runtimematrix.Compatible
		} else if _, ok := p.entries[index].incompatible[version]; ok {
			observation.Version, observation.Compatibility = version, runtimematrix.Incompatible
		}
		return observation, nil
	case runtimeprobe.UnrecognizedOutput, runtimeprobe.CommandFailed, runtimeprobe.TimedOut:
		if report.DetectedVersion() != "" {
			return runtimematrix.Observation{}, errors.New("invalid runtime compatibility input")
		}
		observation.Present = true
		return observation, nil
	default:
		return runtimematrix.Observation{}, errors.New("invalid runtime compatibility input")
	}
}

func (p Policy) valid() bool {
	for _, current := range p.entries {
		if current.compatible == nil || current.incompatible == nil {
			return false
		}
	}
	return true
}

func runtimeIndex(id runtimematrix.RuntimeID) (int, bool) {
	for index, expected := range runtimeOrder {
		if id == expected {
			return index, true
		}
	}
	return 0, false
}

func validVersion(version string) bool {
	return len(version) > 0 && len(version) <= 128 && normalizedVersion.MatchString(version)
}
