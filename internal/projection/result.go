// Package projection defines runtime projection assessment contracts.
package projection

import (
	"errors"
	"strings"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// Result is the declarative outcome of projecting Cortex-owned selection.
type Result string

const (
	// Exact means the runtime represents the selection without translation.
	Exact Result = "exact"
	// Translated means the runtime uses a disclosed equivalent representation.
	Translated Result = "translated"
	// Unrepresentable means no equivalent projection is produced.
	Unrepresentable Result = "unrepresentable"
)

// Assessment immutably binds one projection result to a runtime and catalog snapshot.
type Assessment struct {
	runtimeID           runtimematrix.RuntimeID
	snapshotFingerprint string
	result              Result
	disclosure          string
}

// NewAssessment returns a validated immutable projection assessment.
func NewAssessment(runtimeID runtimematrix.RuntimeID, snapshotFingerprint string, result Result, disclosure string) (Assessment, error) {
	assessment := Assessment{
		runtimeID:           runtimeID,
		snapshotFingerprint: snapshotFingerprint,
		result:              result,
		disclosure:          disclosure,
	}
	if !validAssessment(assessment) {
		return Assessment{}, errors.New("projection assessment: invalid input")
	}
	return assessment, nil
}

// RuntimeID returns the supported runtime bound to the assessment.
func (assessment Assessment) RuntimeID() runtimematrix.RuntimeID { return assessment.runtimeID }

// SnapshotFingerprint returns the catalog snapshot fingerprint bound to the assessment.
func (assessment Assessment) SnapshotFingerprint() string { return assessment.snapshotFingerprint }

// Result returns the declarative projection result.
func (assessment Assessment) Result() Result { return assessment.result }

// TranslationDisclosure returns the disclosed equivalent representation, if any.
func (assessment Assessment) TranslationDisclosure() string { return assessment.disclosure }

// Assessor assesses one catalog snapshot for a declared runtime.
// An assessment does not authorize touching a runtime; adapterplan and future verified
// artifacts remain separate gates.
type Assessor interface {
	RuntimeID() runtimematrix.RuntimeID
	Assess(catalog.CatalogSnapshot) (Assessment, error)
}

// ValidateBinding verifies that an assessment is valid and bound to the expected runtime
// and catalog snapshot. It does not authorize touching a runtime; adapterplan and future
// verified artifacts remain separate gates.
func ValidateBinding(snapshot catalog.CatalogSnapshot, expectedRuntime runtimematrix.RuntimeID, assessment Assessment) error {
	if !supportedRuntime(expectedRuntime) || !validAssessment(assessment) || assessment.runtimeID != expectedRuntime || assessment.snapshotFingerprint != snapshot.Fingerprint() {
		return errors.New("projection binding: invalid input")
	}
	return nil
}

func validAssessment(assessment Assessment) bool {
	if !supportedRuntime(assessment.runtimeID) || !validFingerprint(assessment.snapshotFingerprint) {
		return false
	}
	switch assessment.result {
	case Exact, Unrepresentable:
		return assessment.disclosure == ""
	case Translated:
		return assessment.disclosure != "" && strings.TrimSpace(assessment.disclosure) == assessment.disclosure
	default:
		return false
	}
}

func supportedRuntime(runtimeID runtimematrix.RuntimeID) bool {
	switch runtimeID {
	case runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode:
		return true
	default:
		return false
	}
}

func validFingerprint(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
