package qapi

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// BoundInvocationPaths is one preflight-verified, in-memory path binding.
type BoundInvocationPaths struct {
	role                      qarole.RoleID
	binary, actor, skill, cwd string
}

// BindInvocationPaths retains only clean absolute paths for one closed role.
// Preflight owns filesystem canonicalization and identity verification.
//
// The actor path is optional, and empty is a meaning rather than an omission:
// two of the three runtimes receive no actor file at all, because neither
// takes one in argv — the actor reaches them inside the bounded stdin frame.
// The backends that do put the actor in argv revalidate it themselves, so
// accepting an empty actor here never lets one reach a runtime that needs it.
// The skill path stays mandatory: every runtime loads it from its own root.
func BindInvocationPaths(role qarole.RoleID, binary, actor, skill, cwd string) (BoundInvocationPaths, error) {
	if _, err := qarole.ValidateSquad([]qarole.RoleID{role}); err != nil || !absolutePaths(binary, skill, cwd) ||
		(actor != "" && !absolutePaths(actor)) {
		return BoundInvocationPaths{}, errors.New("invalid bound invocation paths")
	}
	return BoundInvocationPaths{role: role, binary: binary, actor: actor, skill: skill, cwd: cwd}, nil
}

func absolutePaths(paths ...string) bool {
	for _, path := range paths {
		if path == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return false
		}
	}
	return true
}

// Invocation is the immutable process value for one backend's exact token
// contract, and the only value that crosses the Backend port into the shared
// runner. Keeping it opaque means an adapter chooses the tokens but can never
// choose the environment, the bounds, or the number of turns.
type Invocation struct {
	binary, cwd string
	argv        []string
}

func (invocation Invocation) Binary() string {
	return invocation.binary
}

func (invocation Invocation) CWD() string {
	return invocation.cwd
}

func (invocation Invocation) Argv() []string {
	return append([]string(nil), invocation.argv...)
}

// BuildInvocation constructs no process, shell, filesystem, or provider call.
// It is the Pi backend's token contract, reached through Backend.BuildInvocation.
func BuildInvocation(route qaroute.ResolvedRoute, binding BoundInvocationPaths) (Invocation, error) {
	if !validRoute(route) || route.Role != binding.role || !absolutePaths(binding.binary, binding.actor, binding.skill, binding.cwd) {
		return Invocation{}, errors.New("invalid Pi invocation binding")
	}
	return Invocation{
		binary: binding.binary,
		cwd:    binding.cwd,
		argv: []string{
			"--model", route.Provider + "/" + route.Model,
			"--thinking", route.Effort,
			"--append-system-prompt", binding.actor,
			"--skill", binding.skill,
			"--mode", "json",
			"--print",
			"--no-session",
			"--no-extensions",
			"--no-context-files",
			"--no-tools",
		},
	}, nil
}

// validRoute pins one resolved route to the Pi backend. It re-resolves rather
// than trusting the value it was handed, so a route that was mutated after
// resolution — or resolved for another backend — cannot reach Pi's argv.
func validRoute(route qaroute.ResolvedRoute) bool {
	return validRouteFor(route, piBackendID)
}

// validRouteFor pins one resolved route to one backend. It re-resolves rather
// than trusting the value it was handed, so a route that was mutated after
// resolution — or resolved for another backend — cannot reach that backend's
// argv or input frame. A backend the route policy does not admit fails here,
// which is what keeps a landed adapter unreachable until admitting it is a
// deliberate policy change in qaroute.
func validRouteFor(route qaroute.ResolvedRoute, backendID string) bool {
	allowed, failure := qaroute.Resolve(qaroute.Request{Role: route.Role, Backend: backendID}, qaroute.Snapshot{})
	return failure.Code == "" && route.PolicyVersion == qaroute.PolicyVersion && route.Backend == backendID && route.Provider == allowed.Provider && route.Model == allowed.Model && route.Effort == allowed.Effort
}
