// Package ownership builds neutral plans for generated artifact ownership.
package ownership

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/refactor-ia/cortex/internal/artifact"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// Ownership identifies the owner of an observed logical artifact.
type Ownership string

const (
	CortexOwned Ownership = "cortex-owned"
	UserOwned   Ownership = "user-owned"
	Unrelated   Ownership = "unrelated"
)

// Action is the neutral lifecycle intent for an artifact.
type Action string

const (
	Create    Action = "create"
	Replace   Action = "replace"
	Remove    Action = "remove"
	Unchanged Action = "unchanged"
	Preserve  Action = "preserve"
	Conflict  Action = "conflict"
)

// ObservedArtifact records one present provider-observed logical slot.
type ObservedArtifact struct {
	LogicalID   string
	CurrentHash string
	Ownership   Ownership
}

// Decision records neutral ownership intent for one logical artifact.
type Decision struct {
	LogicalID         string
	ObservedOwnership Ownership
	DesiredHash       string
	CurrentHash       string
	Action            Action
	TouchAllowed      bool
}

// Plan is an immutable ownership plan. It does not authorize materialization.
type Plan struct {
	decisions  []Decision
	ready      bool
	hasChanges bool
}

// Decisions returns a detached, non-nil canonical decision slice.
func (plan Plan) Decisions() []Decision { return append([]Decision{}, plan.decisions...) }

// Ready reports whether no ownership conflict was found.
func (plan Plan) Ready() bool { return plan.ready }

// HasChanges reports whether a ready plan contains a create, replace, or remove.
func (plan Plan) HasChanges() bool { return plan.hasChanges }

// Build validates a bundle and observed slots, then derives neutral ownership intent.
func Build(bundle artifact.Bundle, observed []ObservedArtifact) (Plan, error) {
	desired, ok := validBundle(bundle)
	if !ok {
		return Plan{}, invalid()
	}
	byID := make(map[string]ObservedArtifact, len(observed))
	for _, item := range observed {
		if !validID(item.LogicalID) || !validHash(item.CurrentHash) || !validOwnership(item.Ownership) {
			return Plan{}, invalid()
		}
		if _, duplicate := byID[item.LogicalID]; duplicate {
			return Plan{}, invalid()
		}
		byID[item.LogicalID] = item
	}
	byDesired := make(map[string]string, len(desired))
	for _, item := range desired {
		byDesired[item.LogicalID()] = item.SHA256()
	}
	ids := make([]string, 0, len(byDesired)+len(byID))
	for id := range byDesired {
		ids = append(ids, id)
	}
	for id := range byID {
		if _, exists := byDesired[id]; !exists {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	decisions, conflict, changes := make([]Decision, 0, len(ids)), false, false
	for _, id := range ids {
		desiredHash, wanted := byDesired[id]
		item, present := byID[id]
		decision := Decision{LogicalID: id, DesiredHash: desiredHash}
		switch {
		case wanted && !present:
			decision.ObservedOwnership, decision.Action, decision.TouchAllowed = CortexOwned, Create, true
		case wanted && item.Ownership == CortexOwned && item.CurrentHash == desiredHash:
			decision.ObservedOwnership, decision.CurrentHash, decision.Action = item.Ownership, item.CurrentHash, Unchanged
		case wanted && item.Ownership == CortexOwned:
			decision.ObservedOwnership, decision.CurrentHash, decision.Action, decision.TouchAllowed = item.Ownership, item.CurrentHash, Replace, true
		case wanted:
			decision.ObservedOwnership, decision.CurrentHash, decision.Action = item.Ownership, item.CurrentHash, Conflict
			conflict = true
		case item.Ownership == CortexOwned:
			decision.ObservedOwnership, decision.CurrentHash, decision.Action, decision.TouchAllowed = item.Ownership, item.CurrentHash, Remove, true
		default:
			decision.ObservedOwnership, decision.CurrentHash, decision.Action = item.Ownership, item.CurrentHash, Preserve
		}
		changes = changes || decision.Action == Create || decision.Action == Replace || decision.Action == Remove
		decisions = append(decisions, decision)
	}
	if conflict {
		for index := range decisions {
			decisions[index].TouchAllowed = false
		}
		return Plan{decisions: decisions}, nil
	}
	return Plan{decisions: decisions, ready: true, hasChanges: changes}, nil
}

func invalid() error { return errors.New("ownership plan: invalid input") }

func validBundle(bundle artifact.Bundle) ([]artifact.BundledArtifact, bool) {
	manifest, artifacts := bundle.Manifest(), bundle.Artifacts()
	if manifest.SchemaVersion() != 1 || manifest.Owner() != "cortex" || !validHash(manifest.SnapshotFingerprint()) || !supportedRuntime(manifest.RuntimeID()) || len(artifacts) == 0 {
		return nil, false
	}
	if manifest.ProjectionResult() == projection.Exact {
		if manifest.TranslationDisclosure() != "" {
			return nil, false
		}
	} else if manifest.ProjectionResult() != projection.Translated || manifest.TranslationDisclosure() == "" || strings.TrimSpace(manifest.TranslationDisclosure()) != manifest.TranslationDisclosure() || strings.ContainsAny(manifest.TranslationDisclosure(), "\r\n\u2028\u2029") {
		return nil, false
	}
	declared := manifest.Artifacts()
	if len(declared) != len(artifacts) {
		return nil, false
	}
	for index, item := range artifacts {
		if !validID(item.LogicalID()) || !validHash(item.SHA256()) || hash(item.Content()) != item.SHA256() || item.LogicalID() != declared[index].LogicalID() || item.SHA256() != declared[index].SHA256() || (index > 0 && artifacts[index-1].LogicalID() >= item.LogicalID()) {
			return nil, false
		}
	}
	return artifacts, true
}

func supportedRuntime(id runtimematrix.RuntimeID) bool {
	return id == runtimematrix.RuntimePi || id == runtimematrix.RuntimeOpenCode || id == runtimematrix.RuntimeClaudeCode
}

func validOwnership(value Ownership) bool {
	return value == CortexOwned || value == UserOwned || value == Unrelated
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

func validID(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\r\n\u2028\u2029") {
		return false
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment[0] == '-' || segment[len(segment)-1] == '-' || strings.Contains(segment, "--") {
			return false
		}
		for _, character := range segment {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
