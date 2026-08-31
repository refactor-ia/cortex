package installplan

import (
	"bytes"
	"path/filepath"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// BuildActorAware creates a detached v2 Pi candidate without accessing external state.
func BuildActorAware(skills Plan, actors qaactor.Binding, installationID installstate.InstallationID) (Plan, error) {
	if !validBoundPiSkills(skills) {
		return Plan{}, invalid()
	}
	actorPlan, err := qaactor.PlanDestinations(actors)
	if err != nil || actors.CatalogFingerprint() != skills.snapshotFingerprint {
		return Plan{}, invalid()
	}

	skillFiles := skills.Files()
	skillFiles = skillFiles[:len(skillFiles)-1]
	destinations := actorPlan.Destinations()
	inputs := make([]installstate.V2ArtifactInput, 0, len(skillFiles)+len(destinations))
	files := make([]File, 0, len(skillFiles)+len(destinations)+1)
	for _, skill := range skillFiles {
		inputs = append(inputs, installstate.V2ArtifactInput{
			LogicalID:      skill.LogicalID(),
			Kind:           installstate.KindSkill,
			CapabilityID:   skill.LogicalID()[len("skills/"):],
			RelativePath:   skill.RelativePath(),
			SHA256:         skill.SHA256(),
			InstallationID: installationID,
		})
		files = append(files, skill.clone())
	}
	for _, destination := range destinations {
		absolutePath := filepath.Join(skills.rootPath, filepath.FromSlash(destination.RelativePath()))
		relativePath, contained := containedRelative(skills.rootPath, absolutePath)
		if !contained || relativePath != destination.RelativePath() {
			return Plan{}, invalid()
		}
		content := destination.Content()
		inputs = append(inputs, installstate.V2ArtifactInput{
			LogicalID:            destination.LogicalID(),
			Kind:                 installstate.KindPiActor,
			RoleID:               destination.RoleID(),
			ActorContractVersion: destination.ActorContract(),
			RelativePath:         relativePath,
			SHA256:               destination.SHA256(),
			InstallationID:       installationID,
		})
		files = append(files, File{
			role:         "actor",
			logicalID:    destination.LogicalID(),
			relativePath: relativePath,
			absolutePath: absolutePath,
			sha256:       destination.SHA256(),
			desiredMode:  CanonicalFileMode,
			content:      content,
		})
	}

	state, err := installstate.NewV2(skills.runtimeID, skills.rootKind, skills.snapshotFingerprint, installationID, inputs)
	if err != nil {
		return Plan{}, invalid()
	}
	stateJSON, err := installstate.Encode(state)
	stateAbsolutePath := filepath.Join(skills.rootPath, filepath.FromSlash(stateRelativePath))
	stateRelative, contained := containedRelative(skills.rootPath, stateAbsolutePath)
	if err != nil || !contained || stateRelative != stateRelativePath {
		return Plan{}, invalid()
	}
	files = append(files, File{
		role:         "state",
		logicalID:    "state/install-state",
		relativePath: stateRelative,
		absolutePath: stateAbsolutePath,
		sha256:       digest(stateJSON),
		desiredMode:  CanonicalFileMode,
		content:      append([]byte(nil), stateJSON...),
	})
	return Plan{
		runtimeID:           skills.runtimeID,
		rootKind:            skills.rootKind,
		snapshotFingerprint: skills.snapshotFingerprint,
		rootPath:            skills.rootPath,
		installedState:      state,
		stateJSON:           append([]byte(nil), stateJSON...),
		files:               files,
		bundle:              skills.bundle,
		hasBundle:           true,
	}, nil
}

func validBoundPiSkills(plan Plan) bool {
	if !plan.hasBundle || plan.runtimeID != runtimematrix.RuntimePi || plan.rootKind != skilldest.RootKindPiUserAgent ||
		!validPath(plan.rootPath) || !validHash(plan.snapshotFingerprint) || len(plan.files) < 2 || !matchesBundle(plan, plan.bundle) {
		return false
	}
	state, err := installstate.Decode(plan.stateJSON)
	canonicalState, encodeErr := installstate.Encode(state)
	retainedStateJSON, retainedStateErr := installstate.Encode(plan.installedState)
	if err != nil || encodeErr != nil || retainedStateErr != nil || state.SchemaVersion() != 1 || state.InstallationID() != "" ||
		!bytes.Equal(plan.stateJSON, canonicalState) || !bytes.Equal(plan.stateJSON, retainedStateJSON) ||
		state.RuntimeID() != plan.runtimeID || state.RootKind() != plan.rootKind || state.SnapshotFingerprint() != plan.snapshotFingerprint {
		return false
	}
	artifacts := state.Artifacts()
	if len(artifacts) != len(plan.files)-1 {
		return false
	}
	for index, artifact := range artifacts {
		file := plan.files[index]
		relativePath, contained := containedRelative(plan.rootPath, file.absolutePath)
		if file.role != "skill" || !validSkill(file.logicalID) || file.relativePath != skillRelative(file.logicalID) || !contained ||
			relativePath != file.relativePath || !validDesiredMode(file.desiredMode) || digest(file.content) != file.sha256 || artifact.LogicalID() != file.logicalID ||
			artifact.RelativePath() != file.relativePath || artifact.SHA256() != file.sha256 || (index > 0 && plan.files[index-1].logicalID >= file.logicalID) {
			return false
		}
	}
	stateFile := plan.files[len(plan.files)-1]
	relativePath, contained := containedRelative(plan.rootPath, stateFile.absolutePath)
	return stateFile.role == "state" && stateFile.logicalID == "state/install-state" && contained && relativePath == stateRelativePath &&
		stateFile.relativePath == stateRelativePath && validDesiredMode(stateFile.desiredMode) && stateFile.sha256 == digest(plan.stateJSON) && bytes.Equal(stateFile.content, plan.stateJSON)
}
