// Package installobserve classifies caller-provided installation observations without I/O.
package installobserve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/ownership"
)

// PriorState is the canonical prior manifest and its observed canonical state-file hash.
type PriorState struct {
	Manifest    installstate.Manifest
	StateSHA256 string
}

// SlotObservation is one caller-provided current skill-slot observation.
type SlotObservation struct {
	LogicalID string
	Present   bool
	SHA256    string
}

// ArtifactDecision is a detached ownership decision with no path, content, or touch authority.
type ArtifactDecision struct {
	LogicalID         string
	Kind              installstate.Kind
	ObservedOwnership ownership.Ownership
	Action            ownership.Action
}

// Result is detached neutral ownership input plus candidate lifecycle decisions.
type Result struct {
	observed          []ownership.ObservedArtifact
	artifactDecisions []ArtifactDecision
	stateAction       ownership.Action
}

// Observed returns a detached, canonical slice suitable for ownership.Build.
func (result Result) Observed() []ownership.ObservedArtifact {
	return append(make([]ownership.ObservedArtifact, 0, len(result.observed)), result.observed...)
}

// ObservedArtifacts is an alias for Observed.
func (result Result) ObservedArtifacts() []ownership.ObservedArtifact { return result.Observed() }

// ArtifactDecisions returns detached, logical-ID-sorted typed ownership decisions.
func (result Result) ArtifactDecisions() []ArtifactDecision { return append([]ArtifactDecision{}, result.artifactDecisions...) }

// StateAction reports create, replace, or unchanged for the candidate state file.
func (result Result) StateAction() ownership.Action { return result.stateAction }

// Classify preserves the v1 in-memory classification API and behavior.
func Classify(candidate installplan.Plan, prior *PriorState, slots []SlotObservation) (Result, error) {
	manifest, stateHash, ok := validV1Candidate(candidate)
	if !ok {
		return Result{}, invalid()
	}
	priorArtifacts := map[string]string(nil)
	if prior != nil {
		var valid bool
		priorArtifacts, valid = validPrior(*prior, candidate, manifest)
		if !valid {
			return Result{}, invalid()
		}
	}
	ids := union(manifest, priorArtifacts)
	if !validSlots(slots, ids) {
		return Result{}, invalid()
	}
	return classify(manifest, stateHash, prior, priorArtifacts, slots), nil
}

// ClassifyFilesystem classifies an observation bound to the exact candidate. V2
// accepts only fresh installation or migration from exact v1 ownership evidence.
func ClassifyFilesystem(candidate installplan.Plan, observation FilesystemObservation) (Result, error) {
	if !observation.MatchesCandidate(candidate) {
		return Result{}, invalid()
	}
	manifest := candidate.InstalledState()
	prior := observation.PriorState()
	if manifest.SchemaVersion() == 1 {
		if !validExactObservation(candidate, observation, prior) {
			return Result{}, invalid()
		}
		return Classify(candidate, prior, observation.Slots())
	}
	if manifest.SchemaVersion() != 2 || prior != nil && prior.Manifest.SchemaVersion() != 1 {
		return Result{}, invalid()
	}
	priorArtifacts := map[string]string(nil)
	if prior != nil {
		var valid bool
		priorArtifacts, valid = validPrior(*prior, candidate, manifest)
		if !valid {
			return Result{}, invalid()
		}
	}
	if !validExactObservation(candidate, observation, prior) {
		return Result{}, invalid()
	}
	for id := range priorArtifacts {
		if exact, found := observation.exact[id]; found && exact.mode != installplan.CanonicalFileMode {
			delete(priorArtifacts, id)
		}
	}
	return classify(manifest, hash(candidate.StateJSON()), prior, priorArtifacts, observation.Slots()), nil
}

func classify(manifest installstate.Manifest, stateHash string, prior *PriorState, priorArtifacts map[string]string, slots []SlotObservation) Result {
	artifacts := artifactMap(manifest)
	decisions := make([]ArtifactDecision, 0, len(slots))
	observed := make([]ownership.ObservedArtifact, 0, len(slots))
	for _, slot := range slots {
		artifact, desired := artifacts[slot.LogicalID]
		kind := installstate.KindSkill
		if desired && artifact.Kind() != "" {
			kind = artifact.Kind()
		}
		owner, action := ownership.Unrelated, ownership.Preserve
		priorHash, owned := priorArtifacts[slot.LogicalID]
		switch {
		case !slot.Present && desired:
			owner, action = ownership.CortexOwned, ownership.Create
		case !slot.Present:
		case owned && slot.SHA256 == priorHash && desired && slot.SHA256 == artifact.SHA256():
			owner, action = ownership.CortexOwned, ownership.Unchanged
		case owned && slot.SHA256 == priorHash && desired:
			owner, action = ownership.CortexOwned, ownership.Replace
		case owned && slot.SHA256 == priorHash:
			owner, action = ownership.CortexOwned, ownership.Remove
		case owned && desired:
			owner, action = ownership.UserOwned, ownership.Conflict
		case owned:
			owner, action = ownership.UserOwned, ownership.Preserve
		case desired:
			owner, action = ownership.Unrelated, ownership.Conflict
		}
		if slot.Present && kind == installstate.KindSkill {
			observed = append(observed, ownership.ObservedArtifact{LogicalID: slot.LogicalID, CurrentHash: slot.SHA256, Ownership: owner})
		}
		decisions = append(decisions, ArtifactDecision{LogicalID: slot.LogicalID, Kind: kind, ObservedOwnership: owner, Action: action})
	}
	action := ownership.Create
	if prior != nil {
		action = ownership.Replace
		if stateHash == prior.StateSHA256 {
			action = ownership.Unchanged
		}
	}
	return Result{observed: observed, artifactDecisions: decisions, stateAction: action}
}

func validV1Candidate(candidate installplan.Plan) (installstate.Manifest, string, bool) {
	manifest, state := candidate.InstalledState(), candidate.StateJSON()
	if manifest.SchemaVersion() != 1 {
		return installstate.Manifest{}, "", false
	}
	encoded, err := installstate.Encode(manifest)
	if err != nil || !bytes.Equal(state, encoded) || candidate.RuntimeID() != manifest.RuntimeID() || candidate.RootKind() != manifest.RootKind() || candidate.SnapshotFingerprint() != manifest.SnapshotFingerprint() || !validPath(candidate.RootPath()) {
		return installstate.Manifest{}, "", false
	}
	artifacts, files := manifest.Artifacts(), candidate.Files()
	if len(artifacts) == 0 || len(files) != len(artifacts)+1 {
		return installstate.Manifest{}, "", false
	}
	for index, artifact := range artifacts {
		file := files[index]
		if file.Role() != "skill" || file.LogicalID() != artifact.LogicalID() || file.RelativePath() != artifact.RelativePath() || file.SHA256() != artifact.SHA256() || hash(file.Content()) != file.SHA256() || file.AbsolutePath() != filepath.Join(candidate.RootPath(), filepath.FromSlash(file.RelativePath())) {
			return installstate.Manifest{}, "", false
		}
	}
	stateFile := files[len(artifacts)]
	if stateFile.Role() != "state" || stateFile.LogicalID() != "state/install-state" || stateFile.RelativePath() != ".cortex/install-state.json" || stateFile.SHA256() != hash(state) || !bytes.Equal(stateFile.Content(), state) || stateFile.AbsolutePath() != filepath.Join(candidate.RootPath(), ".cortex", "install-state.json") {
		return installstate.Manifest{}, "", false
	}
	return manifest, hash(state), true
}

func validPrior(prior PriorState, candidate installplan.Plan, current installstate.Manifest) (map[string]string, bool) {
	encoded, err := installstate.Encode(prior.Manifest)
	if prior.Manifest.SchemaVersion() != 1 || err != nil || prior.StateSHA256 != hash(encoded) || prior.Manifest.RuntimeID() != candidate.RuntimeID() || prior.Manifest.RootKind() != candidate.RootKind() || current.RuntimeID() != prior.Manifest.RuntimeID() {
		return nil, false
	}
	out := make(map[string]string, len(prior.Manifest.Artifacts()))
	for _, artifact := range prior.Manifest.Artifacts() {
		out[artifact.LogicalID()] = artifact.SHA256()
	}
	return out, true
}

func validExactObservation(candidate installplan.Plan, observation FilesystemObservation, prior *PriorState) bool {
	artifacts := artifactMap(candidate.InstalledState())
	priorArtifacts := map[string]string(nil)
	if prior != nil {
		var valid bool
		priorArtifacts, valid = validPrior(*prior, candidate, candidate.InstalledState())
		if !valid {
			return false
		}
		state, found := observation.Exact("state/install-state")
		encoded, err := installstate.Encode(prior.Manifest)
		if !found || err != nil || state.Mode() != installplan.CanonicalFileMode || !bytes.Equal(state.Bytes(), encoded) || hash(state.Bytes()) != prior.StateSHA256 {
			return false
		}
	}
	ids := union(candidate.InstalledState(), priorArtifacts)
	slots := observation.Slots()
	if !validSlots(slots, ids) {
		return false
	}
	for _, slot := range slots {
		exact, found := observation.Exact(slot.LogicalID)
		if slot.Present != found || slot.Present && hash(exact.Bytes()) != slot.SHA256 {
			return false
		}
		if _, desired := artifacts[slot.LogicalID]; !desired && prior == nil {
			return false
		}
	}
	return true
}

func artifactMap(manifest installstate.Manifest) map[string]installstate.Artifact {
	artifacts := manifest.Artifacts()
	out := make(map[string]installstate.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		out[artifact.LogicalID()] = artifact
	}
	return out
}

func union(current installstate.Manifest, prior map[string]string) []string {
	ids, seen := make([]string, 0, len(current.Artifacts())+len(prior)), map[string]bool{}
	for _, artifact := range current.Artifacts() {
		ids, seen[artifact.LogicalID()] = append(ids, artifact.LogicalID()), true
	}
	for id := range prior {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func validSlots(slots []SlotObservation, ids []string) bool {
	if len(slots) != len(ids) {
		return false
	}
	for index, slot := range slots {
		if slot.LogicalID != ids[index] || slot.Present && !validHash(slot.SHA256) || !slot.Present && slot.SHA256 != "" {
			return false
		}
	}
	return true
}

func validPath(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0) && filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) != path && (filepath.Separator != '/' || !strings.Contains(path, "\\"))
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func hash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func invalid() error { return errors.New("install observe: invalid input") }
