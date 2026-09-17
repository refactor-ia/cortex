// Package skillartifact binds projected skills to generated artifact identity.
package skillartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillprojection"
)

const emptyProjection = "empty_projection"
const dormantEnforcementUnavailable = "dormant_enforcement_unavailable"

// Binding is an immutable identity binding for a projected runtime skill set.
type Binding struct {
	manifest artifact.Manifest
	bundle   artifact.Bundle
	json     []byte
	reason   string
	has      bool
}

func (binding Binding) HasArtifacts() bool                  { return binding.has }
func (binding Binding) ReasonCode() string                  { return binding.reason }
func (binding Binding) Manifest() (artifact.Manifest, bool) { return binding.manifest, binding.has }
func (binding Binding) Bundle() (artifact.Bundle, bool)     { return binding.bundle, binding.has }
func (binding Binding) ManifestJSON() []byte                { return append([]byte{}, binding.json...) }

// Build validates matching projections and binds representable projected skills to artifacts.
func Build(projected skillprojection.Plan, final projection.Plan) (Binding, error) {
	assessment := projected.Assessment()
	if !validAssessment(assessment) || assessment.SnapshotFingerprint() != final.SnapshotFingerprint() || !validFinal(final) || !matchesFinal(assessment, final) {
		return Binding{}, invalid()
	}
	skills := projected.Skills()
	if !validSkills(skills) {
		return Binding{}, invalid()
	}
	switch assessment.Result() {
	case projection.Unrepresentable:
		if len(skills) != 0 || projected.ReasonCode() != dormantEnforcementUnavailable {
			return Binding{}, invalid()
		}
		return Binding{reason: dormantEnforcementUnavailable}, nil
	case projection.Exact:
		if projected.ReasonCode() != "" {
			return Binding{}, invalid()
		}
		if len(skills) == 0 {
			return Binding{reason: emptyProjection}, nil
		}
	case projection.Translated:
		if projected.ReasonCode() != "" || len(skills) == 0 {
			return Binding{}, invalid()
		}
	default:
		return Binding{}, invalid()
	}
	artifacts, payloads := make(map[string]string, len(skills)), make([]artifact.PayloadInput, len(skills))
	for index, skill := range skills {
		artifacts[skill.LogicalID()] = skill.SHA256()
		payloads[index] = artifact.PayloadInput{LogicalID: skill.LogicalID(), Content: skill.Content()}
	}
	wire := struct {
		SchemaVersion int                     `json:"schemaVersion"`
		Owner         string                  `json:"owner"`
		Snapshot      string                  `json:"snapshotFingerprint"`
		Runtime       runtimematrix.RuntimeID `json:"runtime"`
		Result        projection.Result       `json:"projectionResult"`
		Disclosure    string                  `json:"translationDisclosure,omitempty"`
		Artifacts     map[string]string       `json:"artifacts"`
	}{1, "cortex", assessment.SnapshotFingerprint(), assessment.RuntimeID(), assessment.Result(), assessment.TranslationDisclosure(), artifacts}
	data, err := json.Marshal(wire)
	if err != nil {
		return Binding{}, invalid()
	}
	manifest, err := artifact.DecodeManifest(data)
	if err != nil {
		return Binding{}, invalid()
	}
	bundle, err := artifact.Bind(manifest, final, payloads)
	if err != nil {
		return Binding{}, invalid()
	}
	return Binding{manifest: manifest, bundle: bundle, json: append([]byte{}, data...), has: true}, nil
}
func invalid() error { return errors.New("skill artifact: invalid binding") }
func validAssessment(a projection.Assessment) bool {
	if !validHash(a.SnapshotFingerprint()) {
		return false
	}
	switch a.RuntimeID() {
	case runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode:
	default:
		return false
	}
	switch a.Result() {
	case projection.Exact, projection.Unrepresentable:
		return a.TranslationDisclosure() == ""
	case projection.Translated:
		return validDisclosure(a.TranslationDisclosure())
	}
	return false
}
func validFinal(plan projection.Plan) bool {
	if !validHash(plan.SnapshotFingerprint()) {
		return false
	}
	results, targets := plan.Results(), plan.TransactionTargets()
	localTarget, local := plan.LocalUpdateTarget()
	admitted := plan.UncertifiedAdmitted()
	ids := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	if len(results) != len(ids) {
		return false
	}
	expected := make([]runtimematrix.RuntimeID, 0, len(ids))
	for index, result := range results {
		if result.ID != ids[index] || !validResult(result, localTarget, admitted) {
			return false
		}
		if result.IncludeInTransaction {
			expected = append(expected, result.ID)
		}
	}
	if local && (localTarget == "" || len(targets) != 1 || targets[0] != localTarget) {
		return false
	}
	if len(targets) != len(expected) {
		return false
	}
	for index := range targets {
		if targets[index] != expected[index] {
			return false
		}
	}
	return plan.AllOrNothing() == (len(targets) > 0) && plan.ReportOnly() == (len(targets) == 0)
}
func validResult(result projection.RuntimeResult, localTarget runtimematrix.RuntimeID, admitted []runtimematrix.RuntimeID) bool {
	// A local update excludes every runtime but its single selected target,
	// including runtimes the plan would otherwise have admitted.
	if localTarget != "" && result.ID != localTarget &&
		(result.Outcome == runtimematrix.OutcomePresentCompatible ||
			result.Outcome == runtimematrix.OutcomePresentUncertified && admits(admitted, result.ID)) {
		return result.ProjectionResult == "" && result.TranslationDisclosure == "" && result.Action == runtimematrix.Configure && !result.IncludeInTransaction && !result.TouchAllowed
	}
	if result.Outcome == runtimematrix.OutcomePresentCompatible || result.Outcome == runtimematrix.OutcomePresentUncertified && admits(admitted, result.ID) {
		switch result.ProjectionResult {
		case projection.Exact:
			return result.TranslationDisclosure == "" && result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
		case projection.Translated:
			return validDisclosure(result.TranslationDisclosure) && result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
		case projection.Unrepresentable:
			return result.TranslationDisclosure == "" && result.Action == runtimematrix.Skip && !result.IncludeInTransaction && !result.TouchAllowed
		}
		return false
	}
	if result.ProjectionResult != "" || result.TranslationDisclosure != "" || result.IncludeInTransaction || result.TouchAllowed {
		return false
	}
	switch result.Outcome {
	case runtimematrix.OutcomeAbsent, runtimematrix.OutcomeUnknownVersion:
		return result.Action == runtimematrix.Warn
	case runtimematrix.OutcomeKnownIncompatible:
		return result.Action == runtimematrix.Skip
	}
	return false
}
func admits(admitted []runtimematrix.RuntimeID, id runtimematrix.RuntimeID) bool {
	for _, candidate := range admitted {
		if candidate == id {
			return true
		}
	}
	return false
}
func matchesFinal(assessment projection.Assessment, plan projection.Plan) bool {
	admitted := plan.UncertifiedAdmitted()
	for _, result := range plan.Results() {
		if result.ID == assessment.RuntimeID() {
			eligible := result.Outcome == runtimematrix.OutcomePresentCompatible || admits(admitted, result.ID) && result.Outcome == runtimematrix.OutcomePresentUncertified
			return eligible && result.ProjectionResult == assessment.Result() && result.TranslationDisclosure == assessment.TranslationDisclosure()
		}
	}
	return false
}
func validSkills(skills []skillprojection.ProjectedSkill) bool {
	previous := ""
	for _, skill := range skills {
		content := skill.Content()
		if !validLogicalID(skill.LogicalID()) || (previous != "" && previous >= skill.LogicalID()) || len(content) == 0 || !utf8.Valid(content) || skill.SHA256() != digest(content) {
			return false
		}
		previous = skill.LogicalID()
	}
	return true
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validLogicalID(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\r\n\u2028\u2029") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part[0] == '-' || part[len(part)-1] == '-' || strings.Contains(part, "--") {
			return false
		}
		for _, c := range part {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return false
			}
		}
	}
	return true
}
func validDisclosure(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\u2028\u2029")
}
func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
