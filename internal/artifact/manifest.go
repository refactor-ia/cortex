// Package artifact defines generated-output identity manifests.
package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// Artifact identifies one generated artifact by its logical ID and content hash.
type Artifact struct {
	logicalID string
	sha256    string
}

// LogicalID returns the runtime-neutral artifact identifier.
func (artifact Artifact) LogicalID() string { return artifact.logicalID }

// SHA256 returns the artifact content hash.
func (artifact Artifact) SHA256() string { return artifact.sha256 }

// Manifest records the identity of generated output for one runtime projection.
type Manifest struct {
	schemaVersion         int
	owner                 string
	snapshotFingerprint   string
	runtimeID             runtimematrix.RuntimeID
	projectionResult      projection.Result
	translationDisclosure string
	artifacts             []Artifact
}

// SchemaVersion returns the manifest schema version.
func (manifest Manifest) SchemaVersion() int { return manifest.schemaVersion }

// Owner returns the manifest owner.
func (manifest Manifest) Owner() string { return manifest.owner }

// SnapshotFingerprint returns the source catalog snapshot fingerprint.
func (manifest Manifest) SnapshotFingerprint() string { return manifest.snapshotFingerprint }

// RuntimeID returns the runtime that received the generated output.
func (manifest Manifest) RuntimeID() runtimematrix.RuntimeID { return manifest.runtimeID }

// ProjectionResult returns the runtime projection outcome.
func (manifest Manifest) ProjectionResult() projection.Result { return manifest.projectionResult }

// TranslationDisclosure returns the preserved translated-projection disclosure, if any.
func (manifest Manifest) TranslationDisclosure() string { return manifest.translationDisclosure }

// Artifacts returns the manifest artifacts in lexicographic logical-ID order.
func (manifest Manifest) Artifacts() []Artifact {
	return append([]Artifact(nil), manifest.artifacts...)
}

type manifestWire struct {
	SchemaVersion         *int                     `json:"schemaVersion"`
	Owner                 *string                  `json:"owner"`
	SnapshotFingerprint   *string                  `json:"snapshotFingerprint"`
	Runtime               *runtimematrix.RuntimeID `json:"runtime"`
	ProjectionResult      *projection.Result       `json:"projectionResult"`
	TranslationDisclosure json.RawMessage          `json:"translationDisclosure"`
	Artifacts             *map[string]string       `json:"artifacts"`
}

// DecodeManifest decodes and validates a version 1 generated artifact manifest.
func DecodeManifest(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire manifestWire
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, errors.New("artifact manifest: invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("artifact manifest: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.Owner == nil || wire.SnapshotFingerprint == nil || wire.Runtime == nil || wire.ProjectionResult == nil || wire.Artifacts == nil {
		return Manifest{}, errors.New("artifact manifest: required field is missing")
	}
	if *wire.SchemaVersion != 1 || *wire.Owner != "cortex" || !validHash(*wire.SnapshotFingerprint) || !supportedRuntime(*wire.Runtime) {
		return Manifest{}, errors.New("artifact manifest: fields are invalid")
	}
	disclosure, ok := validDisclosure(*wire.ProjectionResult, wire.TranslationDisclosure)
	if !ok || len(*wire.Artifacts) == 0 {
		return Manifest{}, errors.New("artifact manifest: projection or artifacts are invalid")
	}
	artifacts := make([]Artifact, 0, len(*wire.Artifacts))
	for id, hash := range *wire.Artifacts {
		if !validLogicalID(id) || !validHash(hash) {
			return Manifest{}, errors.New("artifact manifest: projection or artifacts are invalid")
		}
		artifacts = append(artifacts, Artifact{logicalID: id, sha256: hash})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].logicalID < artifacts[j].logicalID })
	return Manifest{1, "cortex", *wire.SnapshotFingerprint, *wire.Runtime, *wire.ProjectionResult, disclosure, artifacts}, nil
}

func supportedRuntime(runtime runtimematrix.RuntimeID) bool {
	switch runtime {
	case runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode:
		return true
	default:
		return false
	}
}

func validDisclosure(result projection.Result, raw json.RawMessage) (string, bool) {
	if result == projection.Exact {
		return "", raw == nil
	}
	if result != projection.Translated || raw == nil {
		return "", false
	}
	var disclosure string
	if json.Unmarshal(raw, &disclosure) != nil || disclosure == "" || strings.TrimSpace(disclosure) != disclosure || strings.ContainsAny(disclosure, "\r\n\u2028\u2029") {
		return "", false
	}
	return disclosure, true
}

func validHash(value string) bool {
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

func validLogicalID(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\r\n\u2028\u2029") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment[0] == '-' || segment[len(segment)-1] == '-' || strings.Contains(segment, "--") {
			return false
		}
		for _, character := range segment {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}
