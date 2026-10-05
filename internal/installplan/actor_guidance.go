package installplan

import (
	"bytes"
	"io/fs"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
)

// ActorGuidancePrior supplies prior typed ownership and its caller-bound root.
// Neither this value nor detached observations establish filesystem provenance.
type ActorGuidancePrior struct {
	RootPath string
	State    installstate.Manifest
}

// ActorGuidanceObservation is a detached existing actor, never a creation request.
type ActorGuidanceObservation struct {
	LogicalID, RootPath, RelativePath, AbsolutePath string
	Mode                                            fs.FileMode
	Content                                         []byte
}

// BuildActorGuidance composes a trusted canonical BuildActorAware v2 candidate
// with owned v2/v3 actors. Legacy guidance requires explicit adoption; unmarked
// actors still require prior ownership. Returned preimage hashes are keyed by
// actor logical ID for later apply binding, NOT admission or mutation authority.
// Callers MUST authenticate prior state/root, check filesystem root containment
// and symlinks, and obtain fresh observations bound to these preimages at apply.
// No filesystem I/O occurs here. Existing lifecycle gates still reject v3.
func BuildActorGuidance(canonical Plan, prior ActorGuidancePrior, observations []ActorGuidanceObservation, adoptLegacy bool) (Plan, map[string]string, error) {
	stateJSON, err := installstate.Encode(canonical.installedState)
	if err != nil || canonical.installedState.SchemaVersion() != 2 || !canonical.hasBundle || !validPath(canonical.rootPath) ||
		!bytes.Equal(stateJSON, canonical.stateJSON) || canonical.runtimeID != canonical.installedState.RuntimeID() ||
		canonical.rootKind != canonical.installedState.RootKind() || canonical.snapshotFingerprint != canonical.installedState.SnapshotFingerprint() ||
		len(canonical.files) != len(canonical.installedState.Artifacts())+1 {
		return Plan{}, nil, invalid()
	}
	if _, err := installstate.Encode(prior.State); err != nil || (prior.State.SchemaVersion() != 2 && prior.State.SchemaVersion() != 3) ||
		prior.RootPath != canonical.rootPath || prior.State.RuntimeID() != canonical.runtimeID || prior.State.RootKind() != canonical.rootKind ||
		prior.State.InstallationID() != canonical.installedState.InstallationID() {
		return Plan{}, nil, invalid()
	}
	previous := make(map[string]installstate.Artifact)
	for _, a := range prior.State.Artifacts() {
		if a.Kind() == installstate.KindPiActor {
			previous[a.LogicalID()] = a
		}
	}
	observed := make(map[string]ActorGuidanceObservation, len(observations))
	for _, o := range observations {
		if _, duplicate := observed[o.LogicalID]; duplicate || o.RootPath != canonical.rootPath || !validDesiredMode(o.Mode) {
			return Plan{}, nil, invalid()
		}
		observed[o.LogicalID] = o
	}
	if len(observed) != len(previous) {
		return Plan{}, nil, invalid()
	}
	expected := make(map[string]installstate.Artifact)
	for _, a := range canonical.installedState.Artifacts() {
		expected[a.LogicalID()] = a
	}
	files := canonical.Files()
	inputs := make([]installstate.V3ArtifactInput, 0, len(expected))
	preimages := make(map[string]string, len(observed))
	skills := canonical
	skills.files = make([]File, 0, len(files))
	seen := make(map[string]bool)
	for i := range files[:len(files)-1] {
		f := &files[i]
		a, ok := expected[f.logicalID]
		relative, contained := containedRelative(canonical.rootPath, f.absolutePath)
		if !ok || seen[f.logicalID] || !contained || relative != f.relativePath || f.relativePath != a.RelativePath() ||
			!validDesiredMode(f.desiredMode) || f.sha256 != digest(f.content) || f.sha256 != a.SHA256() {
			return Plan{}, nil, invalid()
		}
		seen[f.logicalID] = true
		input := installstate.V3ArtifactInput{V2ArtifactInput: installstate.V2ArtifactInput{
			LogicalID: a.LogicalID(), Kind: a.Kind(), CapabilityID: a.CapabilityID(), RoleID: a.RoleID(),
			ActorContractVersion: a.ActorContractVersion(), RelativePath: a.RelativePath(), SHA256: a.SHA256(), InstallationID: a.InstallationID(),
		}}
		if a.Kind() == installstate.KindSkill {
			if f.role != "skill" || len(preimages) != 0 {
				return Plan{}, nil, invalid()
			}
			skills.files = append(skills.files, *f)
		} else {
			p, owned := previous[f.logicalID]
			o, found := observed[f.logicalID]
			if f.role != "actor" || !owned || !found || o.RelativePath != f.relativePath || o.AbsolutePath != f.absolutePath ||
				p.RelativePath() != a.RelativePath() || p.Kind() != a.Kind() || p.RoleID() != a.RoleID() ||
				p.ActorContractVersion() != a.ActorContractVersion() || p.CapabilityID() != a.CapabilityID() || p.InstallationID() != a.InstallationID() {
				return Plan{}, nil, invalid()
			}
			identity := qaactor.GuidanceIdentity{CanonicalSHA256: p.SHA256(), AdoptLegacyGuidance: adoptLegacy}
			if prior.State.SchemaVersion() == 3 {
				identity.CanonicalSHA256, identity.EffectiveSHA256 = p.CanonicalSHA256(), p.SHA256()
			}
			input.CanonicalSHA256 = digest(f.content) // Trusted NEW render, never reconstructed prefix.
			composed, err := qaactor.ComposeGuidanceWithIdentity(o.Content, f.content, identity)
			if err != nil {
				return Plan{}, nil, err
			}
			f.content, f.sha256 = composed, digest(composed)
			input.SHA256 = f.sha256
			preimages[f.logicalID] = digest(o.Content)
		}
		inputs = append(inputs, input)
	}
	stateFile := &files[len(files)-1]
	relative, contained := containedRelative(canonical.rootPath, stateFile.absolutePath)
	if stateFile.role != "state" || stateFile.logicalID != "state/install-state" || !contained || relative != stateRelativePath ||
		stateFile.relativePath != stateRelativePath || !validDesiredMode(stateFile.desiredMode) || stateFile.sha256 != digest(stateJSON) || !bytes.Equal(stateFile.content, stateJSON) || len(preimages) != len(observed) {
		return Plan{}, nil, invalid()
	}
	skills.files = append(skills.files, *stateFile)
	if !matchesBundle(skills, canonical.bundle) {
		return Plan{}, nil, invalid()
	}
	state, err := installstate.NewV3(canonical.runtimeID, canonical.rootKind, canonical.snapshotFingerprint, canonical.installedState.InstallationID(), inputs)
	if err != nil {
		return Plan{}, nil, err
	}
	encoded, err := installstate.Encode(state)
	if err != nil {
		return Plan{}, nil, err
	}
	stateFile.content, stateFile.sha256 = bytes.Clone(encoded), digest(encoded)
	result := canonical
	result.installedState, result.stateJSON, result.files = state, bytes.Clone(encoded), files
	return result, preimages, nil
}
