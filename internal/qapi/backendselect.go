package qapi

import (
	"context"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// NewBackend constructs the adapter for one admitted backend token. It is the
// one place a backend token becomes a Backend, so a caller outside this
// package selects a backend by naming the policy token rather than by reaching
// for a constructor and, in doing so, deciding policy by which function it
// imported.
//
// The set it answers for is qaroute's, not its own: a token the route policy
// does not admit is refused here even if an adapter for it exists. That keeps
// "which backends may run" one decision in one package, and makes an adapter
// landing in this one insufficient on its own to make it reachable.
//
// A nil resolver selects each adapter's production constrained PATH lookup.
func NewBackend(id string, resolver PathResolver) (Backend, bool) {
	if !qaroute.Admits(id) {
		return nil, false
	}
	switch id {
	case piBackendID:
		return NewPiBackend(resolver), true
	case claudeBackendID:
		return NewClaudeBackend(resolver), true
	case opencodeBackendID:
		return NewOpenCodeBackend(resolver), true
	default:
		return nil, false
	}
}

// ProbeBackendAvailability answers, without running a report, whether one
// backend could run one. It binds the runtime and issues that backend's own
// fixed availability probes through the shared bounded runner — the same two
// steps a report run performs before it launches anything, and nothing after
// them.
//
// It exists so a caller can ask the question on its own. Presence of a runtime
// on PATH is not an answer to it: a runtime can be installed, detected, and
// version-bound while its backend has no usable credentials, and reporting the
// first as though it settled the second is exactly the conflation this
// function refuses to make.
//
// Every refusal is one of the typed admission codes a receipt already uses, so
// a consumer reads one vocabulary rather than two. CodeUnsupportedRuntime
// covers the closed door: an unadmitted backend, an unresolvable role, a
// missing context, or a runtime that could not be bound. A catalog with no
// profile route for the role answers route_incomplete, and an unusable profile
// source profile_invalid, before anything is bound. The probes themselves
// contribute CodeAuthNotReady, CodeModelUnavailable, and
// CodeNormalizationFailed when a probe's own output could not be read.
//
// It is read-only: it launches only the fixed probe commands, never a report,
// and mutates no user configuration.
func ProbeBackendAvailability(ctx context.Context, id string, role qarole.RoleID, catalogRoot, cwd string, resolver PathResolver) AvailabilityVerdict {
	if ctx == nil {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	backend, ok := NewBackend(id, resolver)
	if !ok {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	// The route is the one the catalog profile resolves, never a default
	// (D1, #458): one backend's probe is route-dependent, and roles carry no
	// default route. No catalog or no profile route is route_incomplete; an
	// unknown role stays a closed door.
	if !qaroute.KnownRole(role) {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	if catalogRoot == "" {
		return AvailabilityVerdict{Code: qaadmission.CodeRouteIncomplete}
	}
	route, failure := resolveProfileRoute(catalogRoot, AdmissionRequest{Role: role, Backend: backend.ID()})
	if failure.Code != "" {
		return AvailabilityVerdict{Code: qaadmission.Code(failure.Code)}
	}
	bound, err := backend.Bind(ctx, cwd)
	if err != nil {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	return backend.ProbeAvailability(ctx, bound, route)
}
