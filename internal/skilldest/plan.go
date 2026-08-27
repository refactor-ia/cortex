// Package skilldest plans symbolic user-level skill destinations without I/O.
package skilldest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillartifact"
)

// RootKind identifies a symbolic user configuration root.
type RootKind string

const (
	RootKindPiUserAgent        RootKind = "pi-user-agent"
	RootKindOpenCodeUserConfig RootKind = "opencode-user-config"
	RootKindClaudeCodeUser     RootKind = "claude-code-user"
)

// Plan is an immutable, non-materializing destination plan.
type Plan struct {
	runtimeID           runtimematrix.RuntimeID
	rootKind            RootKind
	snapshotFingerprint string
	destinations        []Destination
}

// RuntimeID returns the bound runtime.
func (plan Plan) RuntimeID() runtimematrix.RuntimeID { return plan.runtimeID }

// RootKind returns the symbolic user root.
func (plan Plan) RootKind() RootKind { return plan.rootKind }

// SnapshotFingerprint returns the bound source snapshot identity.
func (plan Plan) SnapshotFingerprint() string { return plan.snapshotFingerprint }

// Destinations returns a detached, non-nil lexicographic copy.
func (plan Plan) Destinations() []Destination {
	destinations := make([]Destination, len(plan.destinations))
	copy(destinations, plan.destinations)
	return destinations
}

// Destination is one intended path below a symbolic root.
type Destination struct {
	logicalID    string
	relativePath string
	sha256       string
	content      []byte
}

// LogicalID returns the semantic source skill identifier.
func (destination Destination) LogicalID() string { return destination.logicalID }

// RelativePath returns the intended path below the symbolic root.
func (destination Destination) RelativePath() string { return destination.relativePath }

// SHA256 returns the preserved content hash.
func (destination Destination) SHA256() string { return destination.sha256 }

// Content returns a detached, non-nil copy of the projected bytes.
func (destination Destination) Content() []byte {
	content := make([]byte, len(destination.content))
	copy(content, destination.content)
	return content
}

// Build validates an artifact binding and derives intended symbolic destinations.
func Build(binding skillartifact.Binding) (Plan, error) {
	if !binding.HasArtifacts() {
		return Plan{}, invalid()
	}
	manifest, manifestOK := binding.Manifest()
	bundle, bundleOK := binding.Bundle()
	if !manifestOK || !bundleOK || !validManifest(manifest) || !sameManifest(manifest, bundle.Manifest()) {
		return Plan{}, invalid()
	}
	root, rootOK := rootFor(manifest.RuntimeID())
	if !rootOK {
		return Plan{}, invalid()
	}
	declared, bundled := manifest.Artifacts(), bundle.Artifacts()
	if len(declared) == 0 || len(declared) != len(bundled) {
		return Plan{}, invalid()
	}
	destinations := make([]Destination, len(bundled))
	for index, payload := range bundled {
		if !sameArtifact(declared[index], payload) || (index > 0 && bundled[index-1].LogicalID() >= payload.LogicalID()) {
			return Plan{}, invalid()
		}
		capability, valid := capabilityID(payload.LogicalID())
		if !valid || !validHash(payload.SHA256()) {
			return Plan{}, invalid()
		}
		content := payload.Content()
		if digest(content) != payload.SHA256() {
			return Plan{}, invalid()
		}
		relativePath := "skills/cortex-" + capability + "/SKILL.md"
		if !validRelativePath(relativePath, capability) {
			return Plan{}, invalid()
		}
		destinations[index] = Destination{payload.LogicalID(), relativePath, payload.SHA256(), content}
	}
	sort.Slice(destinations, func(i, j int) bool { return destinations[i].logicalID < destinations[j].logicalID })
	return Plan{manifest.RuntimeID(), root, manifest.SnapshotFingerprint(), destinations}, nil
}

func invalid() error { return errors.New("skill destination: invalid binding") }

func validManifest(manifest artifact.Manifest) bool {
	if manifest.SchemaVersion() != 1 || manifest.Owner() != "cortex" || !validHash(manifest.SnapshotFingerprint()) {
		return false
	}
	switch manifest.ProjectionResult() {
	case projection.Exact:
		return manifest.TranslationDisclosure() == ""
	case projection.Translated:
		disclosure := manifest.TranslationDisclosure()
		return disclosure != "" && strings.TrimSpace(disclosure) == disclosure && !strings.ContainsAny(disclosure, "\r\n\u2028\u2029")
	default:
		return false
	}
}

func sameManifest(left, right artifact.Manifest) bool {
	if left.SchemaVersion() != right.SchemaVersion() || left.Owner() != right.Owner() || left.SnapshotFingerprint() != right.SnapshotFingerprint() || left.RuntimeID() != right.RuntimeID() || left.ProjectionResult() != right.ProjectionResult() || left.TranslationDisclosure() != right.TranslationDisclosure() {
		return false
	}
	leftArtifacts, rightArtifacts := left.Artifacts(), right.Artifacts()
	if len(leftArtifacts) != len(rightArtifacts) {
		return false
	}
	for index := range leftArtifacts {
		if leftArtifacts[index].LogicalID() != rightArtifacts[index].LogicalID() || leftArtifacts[index].SHA256() != rightArtifacts[index].SHA256() {
			return false
		}
	}
	return true
}

func sameArtifact(declared artifact.Artifact, payload artifact.BundledArtifact) bool {
	return declared.LogicalID() == payload.LogicalID() && declared.SHA256() == payload.SHA256()
}

func rootFor(runtime runtimematrix.RuntimeID) (RootKind, bool) {
	switch runtime {
	case runtimematrix.RuntimePi:
		return RootKindPiUserAgent, true
	case runtimematrix.RuntimeOpenCode:
		return RootKindOpenCodeUserConfig, true
	case runtimematrix.RuntimeClaudeCode:
		return RootKindClaudeCodeUser, true
	default:
		return "", false
	}
}

func capabilityID(logicalID string) (string, bool) {
	if !strings.HasPrefix(logicalID, "skills/") || strings.Count(logicalID, "/") != 1 {
		return "", false
	}
	capability := strings.TrimPrefix(logicalID, "skills/")
	if capability == "" || len(capability) > 57 || strings.HasPrefix(capability, "cortex-") || capability[0] == '-' || capability[len(capability)-1] == '-' || strings.Contains(capability, "--") {
		return "", false
	}
	for _, character := range capability {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return "", false
		}
	}
	return capability, true
}

func validRelativePath(value, capability string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 3 && parts[0] == "skills" && parts[1] == "cortex-"+capability && parts[2] == "SKILL.md" && !strings.Contains(value, "\\")
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

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
