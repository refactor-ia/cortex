package installstate

import (
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
)

// V3ArtifactInput separates canonical actor identity from full effective SHA256.
// CanonicalSHA256 is required on actors and forbidden on skills.
type V3ArtifactInput struct {
	V2ArtifactInput
	CanonicalSHA256 string
}

// NewV3 validates candidate identity only, not provenance or touch authority.
// No production lifecycle writer uses this schema until composed ownership is supported.
func NewV3(runtimeID runtimematrix.RuntimeID, rootKind skilldest.RootKind, snapshot string, installationID InstallationID, inputs []V3ArtifactInput) (Manifest, error) {
	base := make([]V2ArtifactInput, len(inputs))
	canonical := make(map[string]string, len(inputs))
	for index, input := range inputs {
		base[index] = input.V2ArtifactInput
		canonical[input.LogicalID] = input.CanonicalSHA256
	}
	manifest, err := NewV2(runtimeID, rootKind, snapshot, installationID, base)
	if err != nil {
		return Manifest{}, err
	}
	manifest.schemaVersion = 3
	for index := range manifest.artifacts {
		manifest.artifacts[index].canonicalSHA256 = canonical[manifest.artifacts[index].logicalID]
	}
	if !validV3(manifest) {
		return Manifest{}, invalid()
	}
	return manifest, nil
}

func validV3(manifest Manifest) bool {
	if !validActorState(manifest) {
		return false
	}
	for _, artifact := range manifest.artifacts {
		if artifact.kind == KindPiActor {
			if !validHash(artifact.canonicalSHA256) {
				return false
			}
		} else if artifact.canonicalSHA256 != "" {
			return false
		}
	}
	return true
}
