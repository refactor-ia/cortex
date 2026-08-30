package installstate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"sort"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

const piActorContractVersion = "cortex.qa.pi-actor.v1"

// Kind identifies the ownership category of an artifact.
type Kind string

const (
	KindSkill   Kind = "skill"
	KindPiActor Kind = "pi-actor"
)

// InstallationID identifies one accepted actor-aware installation.
type InstallationID string

// InstallationIDGenerator creates one installation ID from injected entropy.
type InstallationIDGenerator interface {
	Generate() (InstallationID, error)
}

type installationIDGenerator struct {
	reader io.Reader
}

// NewInstallationIDGenerator returns a generator backed by reader.
func NewInstallationIDGenerator(reader io.Reader) InstallationIDGenerator {
	return installationIDGenerator{reader: reader}
}

// DefaultInstallationIDGenerator returns the production entropy-backed generator.
func DefaultInstallationIDGenerator() InstallationIDGenerator {
	return NewInstallationIDGenerator(rand.Reader)
}

func (generator installationIDGenerator) Generate() (InstallationID, error) {
	if generator.reader == nil {
		return "", errors.New("install state: invalid installation ID entropy")
	}
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(generator.reader, bytes); err != nil {
		return "", errors.New("install state: installation ID entropy failed")
	}
	return InstallationID(hex.EncodeToString(bytes)), nil
}

// V2ArtifactInput is one typed, path-neutral ownership entry.
type V2ArtifactInput struct {
	LogicalID            string
	Kind                 Kind
	CapabilityID         string
	RoleID               qarole.RoleID
	ActorContractVersion string
	RelativePath         string
	SHA256               string
	InstallationID       InstallationID
}

// NewV2 validates an actor-aware Pi candidate without encoding or I/O.
func NewV2(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, snapshot string, installationID InstallationID, inputs []V2ArtifactInput) (Manifest, error) {
	artifacts := make([]Artifact, len(inputs))
	for index, input := range inputs {
		artifacts[index] = Artifact{
			logicalID:            input.LogicalID,
			kind:                 input.Kind,
			capabilityID:         input.CapabilityID,
			roleID:               input.RoleID,
			actorContractVersion: input.ActorContractVersion,
			relativePath:         input.RelativePath,
			sha256:               input.SHA256,
			installationID:       input.InstallationID,
		}
	}
	manifest := Manifest{
		schemaVersion:       2,
		owner:               "cortex",
		scope:               "user",
		runtimeID:           runtimeID,
		rootKind:            rootKind,
		snapshotFingerprint: snapshot,
		installationID:      installationID,
		artifacts:           artifacts,
	}
	if !valid(manifest) {
		return Manifest{}, invalid()
	}
	sort.Slice(manifest.artifacts, func(left, right int) bool {
		return manifest.artifacts[left].logicalID < manifest.artifacts[right].logicalID
	})
	return manifest, nil
}

func validV2(manifest Manifest) bool {
	if manifest.owner != "cortex" || manifest.scope != "user" || manifest.runtimeID != runtimematrix.RuntimePi || manifest.rootKind != skilldest.RootKindPiUserAgent || !validHash(manifest.snapshotFingerprint) || !validInstallationID(manifest.installationID) || len(manifest.artifacts) == 0 {
		return false
	}
	logicalIDs := make(map[string]bool, len(manifest.artifacts))
	paths := make(map[string]bool, len(manifest.artifacts))
	roles := make(map[qarole.RoleID]bool, len(qarole.Catalog()))
	skills := 0
	for _, artifact := range manifest.artifacts {
		if !validV2Artifact(artifact) || artifact.installationID != manifest.installationID || logicalIDs[artifact.logicalID] || paths[artifact.relativePath] {
			return false
		}
		logicalIDs[artifact.logicalID] = true
		paths[artifact.relativePath] = true
		switch artifact.kind {
		case KindSkill:
			skills++
		case KindPiActor:
			if roles[artifact.roleID] {
				return false
			}
			roles[artifact.roleID] = true
		}
	}
	return skills > 0 && len(roles) == len(qarole.Catalog())
}

func validV2Artifact(artifact Artifact) bool {
	if !validHash(artifact.sha256) {
		return false
	}
	switch artifact.kind {
	case KindSkill:
		return artifact.roleID == "" && artifact.actorContractVersion == "" && artifact.logicalID == "skills/"+artifact.capabilityID && validCapability(artifact.capabilityID) && artifact.relativePath == "skills/cortex-"+artifact.capabilityID+"/SKILL.md"
	case KindPiActor:
		return artifact.capabilityID == "" && artifact.logicalID == "actors/"+string(artifact.roleID) && validActorRole(artifact.roleID) && artifact.actorContractVersion == piActorContractVersion && artifact.relativePath == "agents/cortex-"+string(artifact.roleID)+".md"
	default:
		return false
	}
}

func validInstallationID(value InstallationID) bool {
	return len(value) == 32 && validHex(string(value))
}

func validActorRole(value qarole.RoleID) bool {
	for _, role := range qarole.Catalog() {
		if value == role.ID {
			return true
		}
	}
	return false
}

func validHex(value string) bool {
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
