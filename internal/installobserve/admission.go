package installobserve

import (
	"bytes"
	"errors"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// Actor provenance names how one admission observed the actor it attests.
//
// The two are not interchangeable and must never be collapsed into one fact.
// ActorProvenanceInstalled means the actor was observed as a file the install
// ledger declares, at a canonical path, with the declared digest, and with no
// shadowing copy anywhere the runtime would look. ActorProvenanceCatalog means
// no such file exists or is consulted, because the runtime takes no actor file:
// the attested digests are the catalog-rendered ones the bounded input frame
// carries. A consumer that treats the second as the first would be claiming an
// installed-artifact guarantee nobody made.
const (
	ActorProvenanceInstalled = "installed"
	ActorProvenanceCatalog   = "catalog"
)

// AdmissionBinding is the caller-supplied catalog projection expected for one
// selected role. It is not inferred from an ambient installation or runtime.
type AdmissionBinding struct {
	Role               qarole.RoleID
	Backend            string
	CatalogFingerprint string
	ActorSHA256        string
	ActorSourceSHA256  string
	ActorBindingSHA256 string
	SkillSHA256        string
}

// AdmissionAssets is an immutable, point-in-time observation of the two exact
// assets required for one admission attempt. It does not guarantee they remain
// unchanged after this call; later checkpoints must re-observe them.
type AdmissionAssets struct {
	installationID, catalogFingerprint, actorSHA256, actorSourceSHA256, actorBindingSHA256, skillSHA256 string
	role                                                                                                qarole.RoleID
	backend, actorProvenance, actorPath, skillPath                                                      string
}

func (assets AdmissionAssets) InstallationID() installstate.InstallationID {
	return installstate.InstallationID(assets.installationID)
}
func (assets AdmissionAssets) CatalogFingerprint() string { return assets.catalogFingerprint }
func (assets AdmissionAssets) RoleID() qarole.RoleID      { return assets.role }
func (assets AdmissionAssets) Backend() string            { return assets.backend }
func (assets AdmissionAssets) ActorSHA256() string        { return assets.actorSHA256 }
func (assets AdmissionAssets) ActorSourceSHA256() string  { return assets.actorSourceSHA256 }
func (assets AdmissionAssets) ActorBindingSHA256() string { return assets.actorBindingSHA256 }
func (assets AdmissionAssets) SkillSHA256() string        { return assets.skillSHA256 }
func (assets AdmissionAssets) ActorPath() string          { return assets.actorPath }

// ActorProvenance reports how the attested actor was observed. It is empty
// only on a zero value, never on an admitted observation.
func (assets AdmissionAssets) ActorProvenance() string { return assets.actorProvenance }

func (assets AdmissionAssets) SkillPath() string { return assets.skillPath }

// ObserveAdmissionAssets reads only the canonical v2 state and the selected
// state-declared assets. It does not create an installation candidate,
// mutate files, select a runtime, or inspect a user home directory.
//
// Which assets those are depends on the backend, and the difference is
// recorded rather than hidden. A backend whose runtime reads the actor from a
// path it is handed must have that actor installed, digest-verified and
// unshadowed. A backend whose runtime never reads an actor file receives none
// from install, so requiring one would be requiring an artifact nobody
// consumes: its actor is attested from catalog provenance instead, and
// ActorProvenance says so. The skill is required either way, because all three
// runtimes load it from their own root.
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
	if err != nil || encodeErr != nil || !bytes.Equal(state, encoded) || !admissibleLedger(manifest, expected.Backend) ||
		manifest.SnapshotFingerprint() != expected.CatalogFingerprint {
		return AdmissionAssets{}, admissionInvalid()
	}
	skill, ok := admissionSkill(manifest, expected)
	if !ok {
		return AdmissionAssets{}, admissionInvalid()
	}
	_, skillPath, ok := admissionFile(root, skill.RelativePath(), skill.SHA256())
	if !ok {
		return AdmissionAssets{}, admissionInvalid()
	}
	assets := AdmissionAssets{
		installationID: string(manifest.InstallationID()), catalogFingerprint: manifest.SnapshotFingerprint(),
		role: expected.Role, backend: expected.Backend, actorProvenance: ActorProvenanceCatalog,
		actorSHA256: expected.ActorSHA256, actorSourceSHA256: expected.ActorSourceSHA256, actorBindingSHA256: expected.ActorBindingSHA256,
		skillSHA256: skill.SHA256(), skillPath: skillPath,
	}
	if !qaroute.BindsInstalledActor(expected.Backend) {
		return assets, nil
	}
	actor, ok := admissionArtifact(manifest, "actors/"+string(expected.Role), installstate.KindPiActor, expected)
	if !ok || actor.ActorContractVersion() != qaactor.ActorContractVersion {
		return AdmissionAssets{}, admissionInvalid()
	}
	actorBytes, actorPath, ok := admissionFile(root, actor.RelativePath(), actor.SHA256())
	if !ok || admissionShadows(root, cwd, expected.Role, actorBytes) != nil {
		return AdmissionAssets{}, admissionInvalid()
	}
	assets.actorProvenance, assets.actorSHA256, assets.actorPath = ActorProvenanceInstalled, actor.SHA256(), actorPath
	return assets, nil
}

// admissibleLedger decides which install-state ledger one backend's admission
// may read, and it is where the two ledger schemas stop being interchangeable.
//
// The ledger must belong to the backend's own runtime, which is the check that
// stops one runtime's root from admitting another's assets now that more than
// one root is reachable.
//
// The schema requirement then follows from what install can actually write.
// Schema v2 is by construction a Pi ledger: installstate rejects a v2 manifest
// whose runtime is not Pi or whose actor set is not the full six roles. A
// runtime that receives skills and no actor therefore has a v1 ledger and can
// never have anything else, so requiring v2 there would be requiring a
// document install cannot produce. v1 is fully validated — canonical skill
// paths, digests, no actor, no installation ID — so what it attests is exact;
// it simply attests less. The installation ID it does not carry is the
// difference, and AdmissionAssets reports it as the empty value rather than
// inventing one.
func admissibleLedger(manifest installstate.Manifest, backend string) bool {
	runtimeID, known := qaroute.RuntimeFor(backend)
	if !known || manifest.RuntimeID() != runtimeID {
		return false
	}
	if qaroute.BindsInstalledActor(backend) {
		return manifest.SchemaVersion() == 2
	}
	return manifest.SchemaVersion() == 1
}

func validAdmissionBinding(binding AdmissionBinding) bool {
	_, err := qarole.ValidateSquad([]qarole.RoleID{binding.Role})
	return err == nil && qaroute.Admits(binding.Backend) && validHash(binding.CatalogFingerprint) &&
		validHash(binding.ActorSHA256) && validHash(binding.ActorSourceSHA256) && validHash(binding.ActorBindingSHA256) && validHash(binding.SkillSHA256)
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

// admissionSkill selects the one declared skill for the admitted role. A v2
// ledger types and names it; a v1 ledger carries neither a kind nor a
// capability by schema, so the logical ID and the expected digest are the
// whole identity there, and any typed field present would mean the decoder
// produced something its own schema forbids.
func admissionSkill(manifest installstate.Manifest, expected AdmissionBinding) (installstate.Artifact, bool) {
	if manifest.SchemaVersion() == 2 {
		return admissionArtifact(manifest, "skills/"+string(expected.Role), installstate.KindSkill, expected)
	}
	logicalID := "skills/" + string(expected.Role)
	for _, artifact := range manifest.Artifacts() {
		if artifact.LogicalID() != logicalID {
			continue
		}
		return artifact, artifact.Kind() == "" && artifact.CapabilityID() == "" && artifact.RoleID() == "" &&
			artifact.InstallationID() == "" && artifact.SHA256() == expected.SkillSHA256
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
