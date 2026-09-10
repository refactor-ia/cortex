package qapi

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

const readOnlyToolToken = "read,grep,find,ls"

// BoundInvocationPaths is one preflight-verified, in-memory path binding.
type BoundInvocationPaths struct {
	role                      qarole.RoleID
	binary, actor, skill, cwd string
}

// BindInvocationPaths retains only clean absolute paths for one closed role.
// Preflight owns filesystem canonicalization and identity verification.
func BindInvocationPaths(role qarole.RoleID, binary, actor, skill, cwd string) (BoundInvocationPaths, error) {
	if _, err := qarole.ValidateSquad([]qarole.RoleID{role}); err != nil || !absolutePaths(binary, actor, skill, cwd) {
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

// Invocation is the immutable process value for the exact Pi token contract.
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
			"--tools", readOnlyToolToken,
		},
	}, nil
}

func validRoute(route qaroute.ResolvedRoute) bool {
	allowed, failure := qaroute.Resolve(qaroute.Request{Role: route.Role, Backend: "pi"}, qaroute.Snapshot{})
	return failure.Code == "" && route.PolicyVersion == qaroute.PolicyVersion && route.Backend == "pi" && route.Provider == allowed.Provider && route.Model == allowed.Model && route.Effort == allowed.Effort
}
