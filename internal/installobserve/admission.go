package installobserve

import (
	"bytes"
	"errors"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
)

// AdmissionBinding is the caller-supplied catalog projection expected for one
// selected role. It is not inferred from an ambient installation or runtime.
type AdmissionBinding struct {
	Role               qarole.RoleID
	Backend            string
	CatalogFingerprint string
	ActorSHA256        string
	SkillSHA256        string
}

// AdmissionAssets is an immutable, point-in-time observation of the two exact
// assets required for one admission attempt. It does not guarantee they remain
// unchanged after this call; later checkpoints must re-observe them.
type AdmissionAssets struct {
	installationID, catalogFingerprint, actorSHA256, skillSHA256 string
	role                                                         qarole.RoleID
	backend, actorPath, skillPath                                string
}

func (assets AdmissionAssets) InstallationID() installstate.InstallationID {
	return installstate.InstallationID(assets.installationID)
}
func (assets AdmissionAssets) CatalogFingerprint() string { return assets.catalogFingerprint }
func (assets AdmissionAssets) RoleID() qarole.RoleID      { return assets.role }
func (assets AdmissionAssets) Backend() string            { return assets.backend }
func (assets AdmissionAssets) ActorSHA256() string        { return assets.actorSHA256 }
func (assets AdmissionAssets) SkillSHA256() string        { return assets.skillSHA256 }
func (assets AdmissionAssets) ActorPath() string          { return assets.actorPath }
func (assets AdmissionAssets) SkillPath() string          { return assets.skillPath }

// ObserveAdmissionAssets reads only the canonical v2 state and the selected
// state-declared actor and skill. It does not create an installation candidate,
// mutate files, select a runtime, or inspect a user home directory.
func ObserveAdmissionAssets(root, cwd string, expected AdmissionBinding) (AdmissionAssets, error) {
	if !validAdmissionBinding(expected) || validateShadowRoot(root) != nil || validateShadowRoot(cwd) != nil {
		return AdmissionAssets{}, admissionInvalid()
	}
	state, mode, present, err := readRegular(root, ".cortex/install-state.json", DefaultMaxStateBytes)
	if err != nil || !present || mode != installplan.CanonicalFileMode {
		return AdmissionAssets{}, admissionInvalid()
	}
	manifest, err := installstate.Decode(state)
	encoded, encodeErr := installstate.Encode(manifest)
	if err != nil || encodeErr != nil || !bytes.Equal(state, encoded) || manifest.SchemaVersion() != 2 ||
		manifest.SnapshotFingerprint() != expected.CatalogFingerprint {
		return AdmissionAssets{}, admissionInvalid()
	}
	actor, ok := admissionArtifact(manifest, "actors/"+string(expected.Role), installstate.KindPiActor, expected)
	if !ok || actor.ActorContractVersion() != qaactor.ActorContractVersion {
		return AdmissionAssets{}, admissionInvalid()
	}
	skill, ok := admissionArtifact(manifest, "skills/"+string(expected.Role), installstate.KindSkill, expected)
	if !ok {
		return AdmissionAssets{}, admissionInvalid()
	}
	actorBytes, actorPath, ok := admissionFile(root, actor.RelativePath(), actor.SHA256())
	if !ok {
		return AdmissionAssets{}, admissionInvalid()
	}
	_, skillPath, ok := admissionFile(root, skill.RelativePath(), skill.SHA256())
	if !ok || admissionShadows(root, cwd, expected.Role, actorBytes) != nil {
		return AdmissionAssets{}, admissionInvalid()
	}
	return AdmissionAssets{
		installationID: string(manifest.InstallationID()), catalogFingerprint: manifest.SnapshotFingerprint(),
		role: expected.Role, backend: expected.Backend, actorSHA256: actor.SHA256(), skillSHA256: skill.SHA256(),
		actorPath: actorPath, skillPath: skillPath,
	}, nil
}

func validAdmissionBinding(binding AdmissionBinding) bool {
	_, err := qarole.ValidateSquad([]qarole.RoleID{binding.Role})
	return err == nil && binding.Backend == "pi" && validHash(binding.CatalogFingerprint) &&
		validHash(binding.ActorSHA256) && validHash(binding.SkillSHA256)
}

func admissionArtifact(manifest installstate.Manifest, logicalID string, kind installstate.Kind, expected AdmissionBinding) (installstate.Artifact, bool) {
	for _, artifact := range manifest.Artifacts() {
		if artifact.LogicalID() != logicalID || artifact.Kind() != kind || artifact.InstallationID() != manifest.InstallationID() {
			continue
		}
		if kind == installstate.KindPiActor {
			return artifact, artifact.RoleID() == expected.Role && artifact.SHA256() == expected.ActorSHA256
		}
		return artifact, artifact.CapabilityID() == string(expected.Role) && artifact.SHA256() == expected.SkillSHA256
	}
	return installstate.Artifact{}, false
}

func admissionFile(root, relative, expectedHash string) ([]byte, string, bool) {
	data, mode, present, err := readRegular(root, relative, DefaultMaxFileBytes)
	if err != nil || !present || mode != installplan.CanonicalFileMode || hash(data) != expectedHash {
		return nil, "", false
	}
	return data, filepath.Join(root, filepath.FromSlash(relative)), true
}

func admissionInvalid() error { return errors.New("install observe: admission assets unavailable") }
