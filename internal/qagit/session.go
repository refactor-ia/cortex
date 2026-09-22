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
)

// SessionFailure is the only error PlanSession returns.
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
// nothing here creates, removes, or touches anything on disk.
type SessionPlan struct {
	Repository, Revision, Path, Marker string
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
		Repository: request.Repository,
		Revision:   request.Revision,
		Path:       path,
		Marker:     sessionMarker(request.Revision, path),
	}, nil
}

// CreateCommand returns the exact Git invocation that would realize the plan.
// It returns the command; it does not run it.
//
// --detach keeps the worktree off every branch, so nothing here can advance a
// ref. --no-track refuses the upstream a detached checkout would not use
// anyway, and "--" ends the option list so a path can never be read as a flag.
func (plan SessionPlan) CreateCommand() Command {
	return gitCommand(plan.Repository, []string{
		"-C", plan.Repository, "worktree", "add", "--detach", "--no-track", "--", plan.Path, plan.Revision,
	})
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
