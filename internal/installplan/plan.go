// Package installplan creates ordered in-memory installation candidates.
package installplan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

const stateRelativePath = ".cortex/install-state.json"

// CanonicalFileMode is the restrictive user-owned mode for every installed
// regular file. It is compatible with Pi, OpenCode, and Claude Code.
const CanonicalFileMode fs.FileMode = 0o600

// Plan is an immutable, non-materialized runtime installation candidate.
type Plan struct {
	runtimeID                     runtimematrix.RuntimeID
	rootKind                      skilldest.RootKind
	snapshotFingerprint, rootPath string
	installedState                installstate.Manifest
	stateJSON                     []byte
	files                         []File
	bundle                        artifact.Bundle
	hasBundle                     bool
}

func (plan Plan) RuntimeID() runtimematrix.RuntimeID    { return plan.runtimeID }
func (plan Plan) RootKind() skilldest.RootKind          { return plan.rootKind }
func (plan Plan) SnapshotFingerprint() string           { return plan.snapshotFingerprint }
func (plan Plan) RootPath() string                      { return plan.rootPath }
func (plan Plan) InstalledState() installstate.Manifest { return plan.installedState }
func (plan Plan) StateJSON() []byte                     { return append([]byte{}, plan.stateJSON...) }

// Bundle returns the trusted desired artifact binding when this plan was built
// with BuildWithBundle. The returned immutable value grants no touch authority.
func (plan Plan) Bundle() (artifact.Bundle, bool) { return plan.bundle, plan.hasBundle }

// Files returns a detached, non-nil copy. State-last is ordering intent only:
// this candidate is not committed, installed, or owned, and TouchAllowed is absent.
func (plan Plan) Files() []File {
	files := make([]File, len(plan.files))
	for index, file := range plan.files {
		files[index] = file.clone()
	}
	return files
}

// File is one candidate file with only in-memory absolute-path information.
type File struct {
	role, logicalID, relativePath, absolutePath, sha256 string
	desiredMode                                         fs.FileMode
	content                                             []byte
}

func (file File) Role() string         { return file.role }
func (file File) LogicalID() string    { return file.logicalID }
func (file File) RelativePath() string { return file.relativePath }
func (file File) AbsolutePath() string { return file.absolutePath }
func (file File) SHA256() string       { return file.sha256 }

// DesiredMode returns the authoritative regular-file mode for this candidate.
func (file File) DesiredMode() fs.FileMode { return file.desiredMode }
func (file File) Content() []byte          { return append([]byte{}, file.content...) }
func (file File) clone() File              { file.content = file.Content(); return file }

// Build plans skills followed by state without accessing filesystem or runtime state.
func Build(resolved skillroot.Plan) (Plan, error) {
	root, targets := resolved.RootPath(), resolved.Targets()
	if !validPath(root) || !validPair(resolved.RuntimeID(), resolved.RootKind()) || !validHash(resolved.SnapshotFingerprint()) || len(targets) == 0 {
		return Plan{}, invalid()
	}
	files := make([]File, len(targets), len(targets)+1)
	inputs := make([]installstate.ArtifactInput, len(targets))
	for index, target := range targets {
		relative, ok := containedRelative(root, target.AbsolutePath())
		id := target.LogicalID()
		if !ok || !validSkill(id) || relative != skillRelative(id) || !validHash(target.SHA256()) || len(target.Content()) == 0 || digest(target.Content()) != target.SHA256() || (index > 0 && targets[index-1].LogicalID() >= id) {
			return Plan{}, invalid()
		}
		files[index] = File{"skill", id, relative, target.AbsolutePath(), target.SHA256(), CanonicalFileMode, target.Content()}
		inputs[index] = installstate.ArtifactInput{LogicalID: id, RelativePath: relative, SHA256: target.SHA256()}
	}
	state, err := installstate.New(resolved.RuntimeID(), resolved.RootKind(), resolved.SnapshotFingerprint(), inputs)
	if err != nil || !sameState(state, resolved, files) {
		return Plan{}, invalid()
	}
	stateJSON, err := installstate.Encode(state)
	stateAbsolute := filepath.Join(root, filepath.FromSlash(stateRelativePath))
	stateRelative, contained := containedRelative(root, stateAbsolute)
	if err != nil || !contained || stateRelative != stateRelativePath {
		return Plan{}, invalid()
	}
	files = append(files, File{"state", "state/install-state", stateRelativePath, stateAbsolute, digest(stateJSON), CanonicalFileMode, stateJSON})
	return Plan{runtimeID: resolved.RuntimeID(), rootKind: resolved.RootKind(), snapshotFingerprint: resolved.SnapshotFingerprint(), rootPath: root, installedState: state, stateJSON: append([]byte{}, stateJSON...), files: files}, nil
}

// BuildWithBundle retains a trusted artifact binding only when it exactly matches
// the resolved candidate. It does not grant ownership or mutation authority.
func BuildWithBundle(resolved skillroot.Plan, bundle artifact.Bundle) (Plan, error) {
	plan, err := Build(resolved)
	if err != nil || !matchesBundle(plan, bundle) {
		return Plan{}, invalid()
	}
	plan.bundle, plan.hasBundle = bundle, true
	return plan, nil
}

func matchesBundle(plan Plan, bundle artifact.Bundle) bool {
	for _, file := range plan.files {
		if !validDesiredMode(file.desiredMode) {
			return false
		}
	}
	manifest, artifacts := bundle.Manifest(), bundle.Artifacts()
	if manifest.RuntimeID() != plan.RuntimeID() || manifest.SnapshotFingerprint() != plan.SnapshotFingerprint() || len(artifacts) == 0 || len(artifacts) != len(plan.files)-1 {
		return false
	}
	declared := manifest.Artifacts()
	if len(declared) != len(artifacts) {
		return false
	}
	for index, artifact := range artifacts {
		file := plan.files[index]
		if file.role != "skill" || artifact.LogicalID() != file.logicalID || artifact.SHA256() != file.sha256 || !bytes.Equal(artifact.Content(), file.content) || declared[index].LogicalID() != artifact.LogicalID() || declared[index].SHA256() != artifact.SHA256() {
			return false
		}
	}
	return true
}

func sameState(state installstate.Manifest, resolved skillroot.Plan, files []File) bool {
	if state.RuntimeID() != resolved.RuntimeID() || state.RootKind() != resolved.RootKind() || state.SnapshotFingerprint() != resolved.SnapshotFingerprint() {
		return false
	}
	artifacts := state.Artifacts()
	if len(artifacts) != len(files) {
		return false
	}
	for index, artifact := range artifacts {
		file := files[index]
		if artifact.LogicalID() != file.logicalID || artifact.RelativePath() != file.relativePath || artifact.SHA256() != file.sha256 {
			return false
		}
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
	}
	return false
}
func validSkill(id string) bool {
	name := strings.TrimPrefix(id, "skills/")
	if !strings.HasPrefix(id, "skills/") || strings.Count(id, "/") != 1 || len(name) == 0 || len(name) > 57 || name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") || strings.HasPrefix(name, "cortex-") {
		return false
	}
	for _, character := range name {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
func skillRelative(id string) string {
	return "skills/cortex-" + strings.TrimPrefix(id, "skills/") + "/SKILL.md"
}
func containedRelative(root, absolute string) (string, bool) {
	if !validPath(absolute) {
		return "", false
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}
func validPath(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0) && filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) != path && (filepath.Separator != '/' || !strings.Contains(path, "\\")) && (filepath.Separator == '/' || !strings.Contains(path, "/"))
}
func validDesiredMode(mode fs.FileMode) bool {
	return mode == CanonicalFileMode
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
func digest(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
func invalid() error               { return errors.New("install plan: invalid candidate") }
