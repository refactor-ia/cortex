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

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// ArtifactInput is caller-supplied identity for one logical skill artifact.
type ArtifactInput struct{ LogicalID, RelativePath, SHA256 string }

// Artifact is one immutable logical artifact identity.
type Artifact struct {
	logicalID, relativePath, sha256 string
	kind                            Kind
	capabilityID                    string
	roleID                          qarole.RoleID
	actorContractVersion            string
	installationID                  InstallationID
}

func (artifact Artifact) LogicalID() string            { return artifact.logicalID }
func (artifact Artifact) RelativePath() string         { return artifact.relativePath }
func (artifact Artifact) SHA256() string               { return artifact.sha256 }
func (artifact Artifact) Kind() Kind                   { return artifact.kind }
func (artifact Artifact) CapabilityID() string         { return artifact.capabilityID }
func (artifact Artifact) RoleID() qarole.RoleID        { return artifact.roleID }
func (artifact Artifact) ActorContractVersion() string { return artifact.actorContractVersion }
func (artifact Artifact) InstallationID() InstallationID {
	return artifact.installationID
}

// Manifest is an immutable path-neutral candidate installed-state record.
type Manifest struct {
	schemaVersion       int
	owner, scope        string
	runtimeID           runtimematrix.RuntimeID
	rootKind            skilldest.RootKind
	snapshotFingerprint string
	artifacts           []Artifact
	installationID      InstallationID
}

func (manifest Manifest) SchemaVersion() int                 { return manifest.schemaVersion }
func (manifest Manifest) Owner() string                      { return manifest.owner }
func (manifest Manifest) Scope() string                      { return manifest.scope }
func (manifest Manifest) RuntimeID() runtimematrix.RuntimeID { return manifest.runtimeID }
func (manifest Manifest) RootKind() skilldest.RootKind       { return manifest.rootKind }
func (manifest Manifest) SnapshotFingerprint() string        { return manifest.snapshotFingerprint }
func (manifest Manifest) InstallationID() InstallationID     { return manifest.installationID }

// Artifacts returns a detached, non-nil logical-ID-sorted copy.
func (manifest Manifest) Artifacts() []Artifact {
	return append(make([]Artifact, 0, len(manifest.artifacts)), manifest.artifacts...)
}

// New validates candidate state only; it is not installed proof or touch authority.
func New(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, snapshot string, inputs []ArtifactInput) (Manifest, error) {
	artifacts := make([]Artifact, len(inputs))
	for index, input := range inputs {
		artifacts[index] = Artifact{
			logicalID:    input.LogicalID,
			relativePath: input.RelativePath,
			sha256:       input.SHA256,
		}
	}
	manifest := Manifest{
		schemaVersion:       1,
		owner:               "cortex",
		scope:               "user",
		runtimeID:           runtimeID,
		rootKind:            rootKind,
		snapshotFingerprint: snapshot,
		artifacts:           artifacts,
	}
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

type schemaVersionWire struct {
	SchemaVersion *int `json:"schemaVersion"`
}

// Decode strictly decodes a versioned candidate installed-state manifest.
func Decode(data []byte) (Manifest, error) {
	if !utf8.Valid(data) {
		return Manifest{}, errors.New("install state: invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var version schemaVersionWire
	if err := decoder.Decode(&version); err != nil {
		return Manifest{}, errors.New("install state: invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("install state: trailing JSON")
	}
	if version.SchemaVersion == nil {
		return Manifest{}, errors.New("install state: required field is missing")
	}
	switch *version.SchemaVersion {
	case 1:
		return decodeV1(data)
	case 2:
		return decodeV2(data)
	default:
		return Manifest{}, invalid()
	}
}

func decodeV1(data []byte) (Manifest, error) {
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

type v2ArtifactWire struct {
	Kind                 *Kind           `json:"kind"`
	CapabilityID         *string         `json:"capabilityId"`
	RoleID               *qarole.RoleID  `json:"roleId"`
	ActorContractVersion *string         `json:"actorContractVersion"`
	RelativePath         *string         `json:"relativePath"`
	SHA256               *string         `json:"sha256"`
	InstallationID       *InstallationID `json:"installationId"`
}

type v2ManifestWire struct {
	SchemaVersion       *int                        `json:"schemaVersion"`
	Owner               *string                     `json:"owner"`
	Scope               *string                     `json:"scope"`
	Runtime             *runtimematrix.RuntimeID    `json:"runtime"`
	RootKind            *skilldest.RootKind         `json:"rootKind"`
	SnapshotFingerprint *string                     `json:"snapshotFingerprint"`
	InstallationID      *InstallationID             `json:"installationId"`
	Artifacts           *map[string]*v2ArtifactWire `json:"artifacts"`
}

func decodeV2(data []byte) (Manifest, error) {
	if err := rejectDuplicateV2ArtifactMembers(data); err != nil {
		return Manifest{}, invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire v2ManifestWire
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, errors.New("install state: invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("install state: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.Owner == nil || wire.Scope == nil || wire.Runtime == nil || wire.RootKind == nil || wire.SnapshotFingerprint == nil || wire.InstallationID == nil || wire.Artifacts == nil {
		return Manifest{}, errors.New("install state: required field is missing")
	}
	if *wire.SchemaVersion != 2 || *wire.Owner != "cortex" || *wire.Scope != "user" {
		return Manifest{}, invalid()
	}
	inputs := make([]V2ArtifactInput, 0, len(*wire.Artifacts))
	for logicalID, artifact := range *wire.Artifacts {
		if artifact == nil || artifact.Kind == nil || artifact.RelativePath == nil || artifact.SHA256 == nil || artifact.InstallationID == nil {
			return Manifest{}, invalid()
		}
		input := V2ArtifactInput{
			LogicalID:      logicalID,
			Kind:           *artifact.Kind,
			RelativePath:   *artifact.RelativePath,
			SHA256:         *artifact.SHA256,
			InstallationID: *artifact.InstallationID,
		}
		switch input.Kind {
		case KindSkill:
			if artifact.CapabilityID == nil || artifact.RoleID != nil || artifact.ActorContractVersion != nil {
				return Manifest{}, invalid()
			}
			input.CapabilityID = *artifact.CapabilityID
		case KindPiActor:
			if artifact.CapabilityID != nil || artifact.RoleID == nil || artifact.ActorContractVersion == nil {
				return Manifest{}, invalid()
			}
			input.RoleID = *artifact.RoleID
			input.ActorContractVersion = *artifact.ActorContractVersion
		default:
			return Manifest{}, invalid()
		}
		inputs = append(inputs, input)
	}
	manifest, err := NewV2(*wire.Runtime, *wire.RootKind, *wire.SnapshotFingerprint, *wire.InstallationID, inputs)
	if err != nil {
		return Manifest{}, invalid()
	}
	return manifest, nil
}

func rejectDuplicateV2ArtifactMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("install state: invalid JSON")
	}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return errors.New("install state: invalid JSON")
		}
		if key == "artifacts" {
			if err := scanV2ArtifactMembers(decoder); err != nil {
				return err
			}
			continue
		}
		if err := consumeJSONValue(decoder); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("install state: invalid JSON")
	}
	if token, err = decoder.Token(); err != io.EOF || token != nil {
		return errors.New("install state: invalid JSON")
	}
	return nil
}

func scanV2ArtifactMembers(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return consumeJSONToken(decoder, token)
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("install state: invalid JSON")
		}
		seen[key] = true
		if err := consumeJSONValue(decoder); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("install state: invalid JSON")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	return consumeJSONToken(decoder, token)
}

func consumeJSONToken(decoder *json.Decoder, token json.Token) error {
	object := token == json.Delim('{')
	closing := json.Delim(']')
	if object {
		closing = json.Delim('}')
	} else if token != json.Delim('[') {
		return nil
	}
	for decoder.More() {
		if object {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			if _, ok := key.(string); !ok {
				return errors.New("install state: invalid JSON")
			}
		}
		if err := consumeJSONValue(decoder); err != nil {
			return err
		}
	}
	if token, err := decoder.Token(); err != nil || token != closing {
		return errors.New("install state: invalid JSON")
	}
	return nil
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

type v2ArtifactEncoded struct {
	Kind                 Kind           `json:"kind"`
	CapabilityID         string         `json:"capabilityId,omitempty"`
	RoleID               qarole.RoleID  `json:"roleId,omitempty"`
	ActorContractVersion string         `json:"actorContractVersion,omitempty"`
	RelativePath         string         `json:"relativePath"`
	SHA256               string         `json:"sha256"`
	InstallationID       InstallationID `json:"installationId"`
}

type v2ManifestEncoded struct {
	SchemaVersion       int                          `json:"schemaVersion"`
	Owner               string                       `json:"owner"`
	Scope               string                       `json:"scope"`
	Runtime             runtimematrix.RuntimeID      `json:"runtime"`
	RootKind            skilldest.RootKind           `json:"rootKind"`
	SnapshotFingerprint string                       `json:"snapshotFingerprint"`
	InstallationID      InstallationID               `json:"installationId"`
	Artifacts           map[string]v2ArtifactEncoded `json:"artifacts"`
}

// Encode validates and canonically encodes candidate state.
func Encode(manifest Manifest) ([]byte, error) {
	if !valid(manifest) {
		return nil, invalid()
	}
	switch manifest.schemaVersion {
	case 1:
		artifacts := make(map[string]artifactEncoded, len(manifest.artifacts))
		for _, artifact := range manifest.artifacts {
			artifacts[artifact.logicalID] = artifactEncoded{artifact.relativePath, artifact.sha256}
		}
		return json.Marshal(manifestEncoded{manifest.schemaVersion, manifest.owner, manifest.scope, manifest.runtimeID, manifest.rootKind, manifest.snapshotFingerprint, artifacts})
	case 2:
		artifacts := make(map[string]v2ArtifactEncoded, len(manifest.artifacts))
		for _, artifact := range manifest.artifacts {
			artifacts[artifact.logicalID] = v2ArtifactEncoded{
				Kind:                 artifact.kind,
				CapabilityID:         artifact.capabilityID,
				RoleID:               artifact.roleID,
				ActorContractVersion: artifact.actorContractVersion,
				RelativePath:         artifact.relativePath,
				SHA256:               artifact.sha256,
				InstallationID:       artifact.installationID,
			}
		}
		return json.Marshal(v2ManifestEncoded{
			SchemaVersion:       manifest.schemaVersion,
			Owner:               manifest.owner,
			Scope:               manifest.scope,
			Runtime:             manifest.runtimeID,
			RootKind:            manifest.rootKind,
			SnapshotFingerprint: manifest.snapshotFingerprint,
			InstallationID:      manifest.installationID,
			Artifacts:           artifacts,
		})
	default:
		return nil, invalid()
	}
}

func invalid() error { return errors.New("install state: invalid manifest") }
func valid(manifest Manifest) bool {
	switch manifest.schemaVersion {
	case 1:
		return validV1(manifest)
	case 2:
		return validV2(manifest)
	default:
		return false
	}
}

func validV1(manifest Manifest) bool {
	if manifest.owner != "cortex" || manifest.scope != "user" || !validPair(manifest.runtimeID, manifest.rootKind) || !validHash(manifest.snapshotFingerprint) || manifest.installationID != "" || len(manifest.artifacts) == 0 {
		return false
	}
	seen := make(map[string]bool, len(manifest.artifacts))
	for _, artifact := range manifest.artifacts {
		if !validArtifact(artifact) || artifact.kind != "" || artifact.capabilityID != "" || artifact.roleID != "" || artifact.actorContractVersion != "" || artifact.installationID != "" || seen[artifact.logicalID] {
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
