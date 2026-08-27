// Package installstate defines path-neutral candidate installed-state manifests.
package installstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// ArtifactInput is caller-supplied identity for one logical skill artifact.
type ArtifactInput struct{ LogicalID, RelativePath, SHA256 string }

// Artifact is one immutable logical artifact identity.
type Artifact struct{ logicalID, relativePath, sha256 string }

func (artifact Artifact) LogicalID() string    { return artifact.logicalID }
func (artifact Artifact) RelativePath() string { return artifact.relativePath }
func (artifact Artifact) SHA256() string       { return artifact.sha256 }

// Manifest is an immutable path-neutral candidate installed-state record.
type Manifest struct {
	schemaVersion       int
	owner, scope        string
	runtimeID           runtimematrix.RuntimeID
	rootKind            skilldest.RootKind
	snapshotFingerprint string
	artifacts           []Artifact
}

func (manifest Manifest) SchemaVersion() int                 { return manifest.schemaVersion }
func (manifest Manifest) Owner() string                      { return manifest.owner }
func (manifest Manifest) Scope() string                      { return manifest.scope }
func (manifest Manifest) RuntimeID() runtimematrix.RuntimeID { return manifest.runtimeID }
func (manifest Manifest) RootKind() skilldest.RootKind       { return manifest.rootKind }
func (manifest Manifest) SnapshotFingerprint() string        { return manifest.snapshotFingerprint }

// Artifacts returns a detached, non-nil logical-ID-sorted copy.
func (manifest Manifest) Artifacts() []Artifact {
	return append(make([]Artifact, 0, len(manifest.artifacts)), manifest.artifacts...)
}

// New validates candidate state only; it is not installed proof or touch authority.
func New(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, snapshot string, inputs []ArtifactInput) (Manifest, error) {
	artifacts := make([]Artifact, len(inputs))
	for index, input := range inputs {
		artifacts[index] = Artifact{input.LogicalID, input.RelativePath, input.SHA256}
	}
	manifest := Manifest{1, "cortex", "user", runtimeID, rootKind, snapshot, artifacts}
	if !valid(manifest) {
		return Manifest{}, invalid()
	}
	sort.Slice(manifest.artifacts, func(i, j int) bool { return manifest.artifacts[i].logicalID < manifest.artifacts[j].logicalID })
	return manifest, nil
}

type artifactWire struct {
	RelativePath *string `json:"relativePath"`
	SHA256       *string `json:"sha256"`
}
type manifestWire struct {
	SchemaVersion       *int                      `json:"schemaVersion"`
	Owner               *string                   `json:"owner"`
	Scope               *string                   `json:"scope"`
	Runtime             *runtimematrix.RuntimeID  `json:"runtime"`
	RootKind            *skilldest.RootKind       `json:"rootKind"`
	SnapshotFingerprint *string                   `json:"snapshotFingerprint"`
	Artifacts           *map[string]*artifactWire `json:"artifacts"`
}

// Decode strictly decodes a version 1 candidate installed-state manifest.
func Decode(data []byte) (Manifest, error) {
	if !utf8.Valid(data) {
		return Manifest{}, errors.New("install state: invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire manifestWire
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, errors.New("install state: invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("install state: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.Owner == nil || wire.Scope == nil || wire.Runtime == nil || wire.RootKind == nil || wire.SnapshotFingerprint == nil || wire.Artifacts == nil {
		return Manifest{}, errors.New("install state: required field is missing")
	}
	inputs := make([]ArtifactInput, 0, len(*wire.Artifacts))
	for logicalID, artifact := range *wire.Artifacts {
		if artifact == nil || artifact.RelativePath == nil || artifact.SHA256 == nil {
			return Manifest{}, invalid()
		}
		inputs = append(inputs, ArtifactInput{logicalID, *artifact.RelativePath, *artifact.SHA256})
	}
	if *wire.SchemaVersion != 1 || *wire.Owner != "cortex" || *wire.Scope != "user" {
		return Manifest{}, invalid()
	}
	manifest, err := New(*wire.Runtime, *wire.RootKind, *wire.SnapshotFingerprint, inputs)
	if err != nil {
		return Manifest{}, invalid()
	}
	return manifest, nil
}

type artifactEncoded struct {
	RelativePath string `json:"relativePath"`
	SHA256       string `json:"sha256"`
}
type manifestEncoded struct {
	SchemaVersion       int                        `json:"schemaVersion"`
	Owner               string                     `json:"owner"`
	Scope               string                     `json:"scope"`
	Runtime             runtimematrix.RuntimeID    `json:"runtime"`
	RootKind            skilldest.RootKind         `json:"rootKind"`
	SnapshotFingerprint string                     `json:"snapshotFingerprint"`
	Artifacts           map[string]artifactEncoded `json:"artifacts"`
}

// Encode validates and canonically encodes candidate state.
func Encode(manifest Manifest) ([]byte, error) {
	if !valid(manifest) {
		return nil, invalid()
	}
	artifacts := make(map[string]artifactEncoded, len(manifest.artifacts))
	for _, artifact := range manifest.artifacts {
		artifacts[artifact.logicalID] = artifactEncoded{artifact.relativePath, artifact.sha256}
	}
	return json.Marshal(manifestEncoded{manifest.schemaVersion, manifest.owner, manifest.scope, manifest.runtimeID, manifest.rootKind, manifest.snapshotFingerprint, artifacts})
}

func invalid() error { return errors.New("install state: invalid manifest") }
func valid(manifest Manifest) bool {
	if manifest.schemaVersion != 1 || manifest.owner != "cortex" || manifest.scope != "user" || !validPair(manifest.runtimeID, manifest.rootKind) || !validHash(manifest.snapshotFingerprint) || len(manifest.artifacts) == 0 {
		return false
	}
	seen := make(map[string]bool, len(manifest.artifacts))
	for _, artifact := range manifest.artifacts {
		if !validArtifact(artifact) || seen[artifact.logicalID] {
			return false
		}
		seen[artifact.logicalID] = true
	}
	return true
}
func validPair(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind) bool {
	switch runtimeID {
	case runtimematrix.RuntimePi:
		return rootKind == skilldest.RootKindPiUserAgent
	case runtimematrix.RuntimeOpenCode:
		return rootKind == skilldest.RootKindOpenCodeUserConfig
	case runtimematrix.RuntimeClaudeCode:
		return rootKind == skilldest.RootKindClaudeCodeUser
	default:
		return false
	}
}
func validArtifact(artifact Artifact) bool {
	capability, ok := strings.CutPrefix(artifact.logicalID, "skills/")
	return ok && len(artifact.logicalID) <= 64 && validCapability(capability) && len(artifact.relativePath) <= 80 && artifact.relativePath == "skills/cortex-"+capability+"/SKILL.md" && validHash(artifact.sha256)
}
func validCapability(value string) bool {
	if value == "" || len(value) > 57 || strings.HasPrefix(value, "cortex-") || value[0] == '-' || value[len(value)-1] == '-' || strings.Contains(value, "--") {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
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
