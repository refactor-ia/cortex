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

// Result is detached neutral ownership input plus the candidate state-file action.
type Result struct {
	observed    []ownership.ObservedArtifact
	stateAction ownership.Action
}

// Observed returns a detached, canonical slice suitable for ownership.Build.
func (result Result) Observed() []ownership.ObservedArtifact {
	return append(make([]ownership.ObservedArtifact, 0, len(result.observed)), result.observed...)
}

// ObservedArtifacts is an alias for Observed.
func (result Result) ObservedArtifacts() []ownership.ObservedArtifact { return result.Observed() }

// StateAction reports create, replace, or unchanged for the candidate state file.
func (result Result) StateAction() ownership.Action { return result.stateAction }

// Classify derives neutral ownership observations from validated in-memory inputs only.
func Classify(candidate installplan.Plan, prior *PriorState, slots []SlotObservation) (Result, error) {
	manifest, stateHash, ok := validCandidate(candidate)
	if !ok {
		return Result{}, invalid()
	}
	var priorArtifacts map[string]string
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
	observed := make([]ownership.ObservedArtifact, 0, len(slots))
	for _, slot := range slots {
		if !slot.Present {
			continue
		}
		owner := ownership.Unrelated
		if expected, found := priorArtifacts[slot.LogicalID]; found {
			owner = ownership.UserOwned
			if slot.SHA256 == expected {
				owner = ownership.CortexOwned
			}
		}
		observed = append(observed, ownership.ObservedArtifact{LogicalID: slot.LogicalID, CurrentHash: slot.SHA256, Ownership: owner})
	}
	action := ownership.Create
	if prior != nil {
		action = ownership.Replace
		if stateHash == prior.StateSHA256 {
			action = ownership.Unchanged
		}
	}
	return Result{observed: observed, stateAction: action}, nil
}

func validCandidate(candidate installplan.Plan) (installstate.Manifest, string, bool) {
	manifest, state := candidate.InstalledState(), candidate.StateJSON()
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
	if err != nil || prior.StateSHA256 != hash(encoded) || prior.Manifest.RuntimeID() != candidate.RuntimeID() || prior.Manifest.RootKind() != candidate.RootKind() || current.RuntimeID() != prior.Manifest.RuntimeID() {
		return nil, false
	}
	artifacts := prior.Manifest.Artifacts()
	out := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		out[artifact.LogicalID()] = artifact.SHA256()
	}
	return out, true
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
		if slot.LogicalID != ids[index] || (slot.Present && !validHash(slot.SHA256)) || (!slot.Present && slot.SHA256 != "") {
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
func hash(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func invalid() error           { return errors.New("install observe: invalid input") }
