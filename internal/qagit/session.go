package qagit

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

const sessionContract = "cortex.qa.session-plan.v1"

// SessionFailureCode names why a session plan was refused. A refusal is always
// one of these; a partial or guessed plan is never returned.
type SessionFailureCode string

const (
	SessionRevisionNotImmutable SessionFailureCode = "session_revision_not_immutable"
	SessionTargetNotOwnable     SessionFailureCode = "session_target_not_ownable"
	SessionPlanInvalid          SessionFailureCode = "session_plan_invalid"
)

// SessionFailure is the typed refusal returned by session planning and command
// generation.
type SessionFailure struct {
	Code SessionFailureCode
}

func (failure SessionFailure) Error() string {
	return string(failure.Code)
}

// SessionRequest asks for one disposable worktree. Repository is the canonical
// root the worktree is derived from, Parent is the canonical directory it is
// created under, and SessionID names it within that parent and becomes the
// last path segment.
type SessionRequest struct {
	Repository, Revision, Parent, SessionID string
}

// SessionPlan describes exactly one detached immutable worktree. It is data:
// nothing here creates, removes, or touches anything on disk. Its zero value is
// invalid; only PlanSession returns a command-capable plan.
type SessionPlan struct {
	repository, revision, path, marker string
	validated                          bool
}

// Repository returns the canonical repository root in the plan.
func (plan SessionPlan) Repository() string {
	return plan.repository
}

// Revision returns the immutable object ID in the plan.
func (plan SessionPlan) Revision() string {
	return plan.revision
}

// Path returns the planned worktree path.
func (plan SessionPlan) Path() string {
	return plan.path
}

// Marker returns the deterministic identity for the planned revision and path.
// It is not proof of ownership.
func (plan SessionPlan) Marker() string {
	return plan.marker
}

// PlanSession resolves a request into a plan, or refuses it. The revision must
// already be a full object ID, because a branch, tag, or symbolic ref names
// whatever it points at later rather than one immutable tree.
func PlanSession(request SessionRequest) (SessionPlan, error) {
	if !validOID(request.Revision, 40) && !validOID(request.Revision, 64) {
		return SessionPlan{}, SessionFailure{Code: SessionRevisionNotImmutable}
	}
	if !canonicalDirectory(request.Repository) || !canonicalDirectory(request.Parent) || !validSessionID(request.SessionID) {
		return SessionPlan{}, SessionFailure{Code: SessionTargetNotOwnable}
	}
	path := filepath.Join(request.Parent, request.SessionID)
	// The target must not exist at all. An existing path was created by
	// something else, so taking it over would make ownership unprovable.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return SessionPlan{}, SessionFailure{Code: SessionTargetNotOwnable}
	}
	return SessionPlan{
		repository: request.Repository,
		revision:   request.Revision,
		path:       path,
		marker:     sessionMarker(request.Revision, path),
		validated:  true,
	}, nil
}

func (plan SessionPlan) validate() error {
	if !plan.validated ||
		(!validOID(plan.revision, 40) && !validOID(plan.revision, 64)) ||
		!filepath.IsAbs(plan.repository) || filepath.Clean(plan.repository) != plan.repository ||
		!filepath.IsAbs(plan.path) || filepath.Clean(plan.path) != plan.path ||
		plan.marker != sessionMarker(plan.revision, plan.path) {
		return SessionFailure{Code: SessionPlanInvalid}
	}
	return nil
}

// CreateCommand returns the exact Git invocation that would realize the plan.
// It returns the command; it does not run it. The target's absence is not
// checked here: this command is generated before creation, while the same plan
// must remain usable to generate its later removal command.
//
// --detach keeps the worktree off every branch, so nothing here can advance a
// ref. --no-track refuses the upstream a detached checkout would not use
// anyway, and "--" ends the option list so a path can never be read as a flag.
func (plan SessionPlan) CreateCommand() (Command, error) {
	if err := plan.validate(); err != nil {
		return Command{}, err
	}
	return plan.createCommand(), nil
}

func (plan SessionPlan) createCommand() Command {
	return gitCommand(plan.repository, []string{
		"-C", plan.repository, "worktree", "add", "--detach", "--no-track", "--", plan.path, plan.revision,
	})
}

// RemoveCommand returns the exact Git invocation that would discard the
// worktree. It names the one planned path: removal is exact, never a prune, a
// clean, or a glob. The deterministic marker is an identity, not proof of
// ownership. --force is what lets a disposable worktree be discarded while a
// QA run has left files in it, which is the ordinary case. It does not require
// the target to be absent because the intended lifecycle calls it after
// creation.
func (plan SessionPlan) RemoveCommand() (Command, error) {
	if err := plan.validate(); err != nil {
		return Command{}, err
	}
	return plan.removeCommand(), nil
}

func (plan SessionPlan) removeCommand() Command {
	return gitCommand(plan.repository, []string{
		"-C", plan.repository, "worktree", "remove", "--force", "--", plan.path,
	})
}

// Commands returns every operation a session plan can express, in lifecycle
// order. The set is closed by construction: a plan has no entry point that
// takes an operation, so no caller can name a Git subcommand this package did
// not write.
func (plan SessionPlan) Commands() ([]Command, error) {
	if err := plan.validate(); err != nil {
		return nil, err
	}
	return []Command{plan.createCommand(), plan.removeCommand()}, nil
}

// validSessionID accepts only a single lowercase alphanumeric-and-dash segment,
// so the identifier cannot introduce a separator, a traversal, or a dotfile.
func validSessionID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// sessionMarker returns a deterministic identity for a revision and path. It
// is not proof of ownership; PlanSession's canonical-directory and target-
// absence checks establish the preconditions for the planned lifecycle.
func sessionMarker(revision, path string) string {
	return "session." + framedDigest(sessionContract, revision, path)
}

// framedDigest hashes each value behind its own 8-byte big-endian length, so
// no concatenation of different values can collide with another.
func framedDigest(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
