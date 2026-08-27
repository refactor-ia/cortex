package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// PayloadInput is caller-owned artifact content identified by a logical ID.
type PayloadInput struct {
	LogicalID string
	Content   []byte
}

// BundledArtifact is an immutable manifest-bound artifact payload.
type BundledArtifact struct {
	logicalID string
	sha256    string
	content   string
}

// LogicalID returns the runtime-neutral artifact identifier.
func (artifact BundledArtifact) LogicalID() string { return artifact.logicalID }

// SHA256 returns the hash bound to the artifact content.
func (artifact BundledArtifact) SHA256() string { return artifact.sha256 }

// Content returns a detached, non-nil copy of the bundled content.
func (artifact BundledArtifact) Content() []byte {
	content := make([]byte, len(artifact.content))
	copy(content, artifact.content)
	return content
}

// Bundle proves in-memory payload identity against one manifest and projection plan.
type Bundle struct {
	manifest  Manifest
	artifacts []BundledArtifact
}

// Manifest returns the manifest bound to this bundle.
func (bundle Bundle) Manifest() Manifest { return bundle.manifest }

// Artifacts returns an ordered, detached, non-nil copy of bundled artifacts.
func (bundle Bundle) Artifacts() []BundledArtifact {
	artifacts := make([]BundledArtifact, len(bundle.artifacts))
	copy(artifacts, bundle.artifacts)
	return artifacts
}

// Bind validates a manifest, final projection plan, and exact payload set without I/O.
func Bind(manifest Manifest, plan projection.Plan, payloads []PayloadInput) (Bundle, error) {
	if !validBundleManifest(manifest) || !validBundlePlan(plan) || manifest.SnapshotFingerprint() != plan.SnapshotFingerprint() || !matchesPlan(manifest, plan) {
		return Bundle{}, invalidBundle()
	}

	inputs := make(map[string]PayloadInput, len(payloads))
	for _, payload := range payloads {
		if !validLogicalID(payload.LogicalID) {
			return Bundle{}, invalidBundle()
		}
		if _, duplicate := inputs[payload.LogicalID]; duplicate {
			return Bundle{}, invalidBundle()
		}
		inputs[payload.LogicalID] = payload
	}
	artifacts := manifest.Artifacts()
	if len(inputs) != len(artifacts) {
		return Bundle{}, invalidBundle()
	}

	bound := make([]BundledArtifact, len(artifacts))
	for index, declared := range artifacts {
		payload, found := inputs[declared.LogicalID()]
		if !found || declared.SHA256() != hashContent(payload.Content) {
			return Bundle{}, invalidBundle()
		}
		bound[index] = BundledArtifact{logicalID: declared.LogicalID(), sha256: declared.SHA256(), content: string(payload.Content)}
	}
	return Bundle{manifest: manifest, artifacts: bound}, nil
}

func invalidBundle() error { return errors.New("artifact bundle: invalid input") }

func validBundleManifest(manifest Manifest) bool {
	if manifest.SchemaVersion() != 1 || manifest.Owner() != "cortex" || !validHash(manifest.SnapshotFingerprint()) || !supportedRuntime(manifest.RuntimeID()) {
		return false
	}
	switch manifest.ProjectionResult() {
	case projection.Exact:
		if manifest.TranslationDisclosure() != "" {
			return false
		}
	case projection.Translated:
		if !validBundleDisclosure(manifest.TranslationDisclosure()) {
			return false
		}
	default:
		return false
	}
	artifacts := manifest.Artifacts()
	if len(artifacts) == 0 {
		return false
	}
	for index, artifact := range artifacts {
		if !validLogicalID(artifact.LogicalID()) || !validHash(artifact.SHA256()) || (index > 0 && artifacts[index-1].LogicalID() >= artifact.LogicalID()) {
			return false
		}
	}
	return true
}

func validBundlePlan(plan projection.Plan) bool {
	if !validHash(plan.SnapshotFingerprint()) {
		return false
	}
	results := plan.Results()
	expected := []runtimematrix.RuntimeID{runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode}
	if len(results) != len(expected) {
		return false
	}
	targets := make([]runtimematrix.RuntimeID, 0, len(results))
	for index, result := range results {
		if result.ID != expected[index] || !validBundleResult(result) {
			return false
		}
		if result.IncludeInTransaction {
			targets = append(targets, result.ID)
		}
	}
	actualTargets := plan.TransactionTargets()
	if len(actualTargets) != len(targets) {
		return false
	}
	for index, target := range targets {
		if actualTargets[index] != target {
			return false
		}
	}
	return plan.AllOrNothing() == (len(targets) > 0) && plan.ReportOnly() == (len(targets) == 0)
}

func validBundleResult(result projection.RuntimeResult) bool {
	if result.Outcome == runtimematrix.OutcomePresentCompatible {
		switch result.ProjectionResult {
		case projection.Exact:
			return result.TranslationDisclosure == "" && result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
		case projection.Translated:
			return validBundleDisclosure(result.TranslationDisclosure) && result.Action == runtimematrix.Configure && result.IncludeInTransaction && result.TouchAllowed
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
	default:
		return false
	}
}

func matchesPlan(manifest Manifest, plan projection.Plan) bool {
	matches := 0
	for _, result := range plan.Results() {
		if result.ID != manifest.RuntimeID() {
			continue
		}
		matches++
		if result.ProjectionResult != manifest.ProjectionResult() || result.TranslationDisclosure != manifest.TranslationDisclosure() || result.Action != runtimematrix.Configure || !result.IncludeInTransaction || !result.TouchAllowed {
			return false
		}
	}
	for _, target := range plan.TransactionTargets() {
		if target == manifest.RuntimeID() {
			return matches == 1
		}
	}
	return false
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func validBundleDisclosure(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\u2028\u2029")
}
