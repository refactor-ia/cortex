package qapi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func TestBuildInvocationGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		role qarole.RoleID
	}{
		{"requirements analyst", qarole.RequirementsAnalyst},
		{"test runner", qarole.TestRunner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binding, err := BindInvocationPaths(tc.role, "/synthetic/bin/pi", "/synthetic/agents/cortex-"+string(tc.role)+".md", "/synthetic/skills/cortex-"+string(tc.role)+"/SKILL.md", "/synthetic/worktree")
			if err != nil {
				t.Fatal(err)
			}
			route := resolvedRoute(t, tc.role)
			invocation, err := BuildInvocation(route, binding)
			if err != nil {
				t.Fatal(err)
			}
			want := readInvocationGolden(t, string(tc.role)+".golden")
			if invocation.Binary() != want.binary || invocation.CWD() != want.cwd || !reflect.DeepEqual(invocation.Argv(), want.argv) {
				t.Fatalf("BuildInvocation() = %#v, want %#v", invocation, want)
			}
			argv := invocation.Argv()
			argv[0] = "changed"
			if invocation.Argv()[0] != "--model" {
				t.Fatal("Invocation leaked mutable argv")
			}
		})
	}
}

func TestBuildInvocationRejectsInvalidInput(t *testing.T) {
	valid := func() BoundInvocationPaths {
		binding, err := BindInvocationPaths(qarole.RequirementsAnalyst, "/synthetic/bin/pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree")
		if err != nil {
			t.Fatal(err)
		}
		return binding
	}
	for _, tc := range []struct {
		name   string
		binary string
		actor  string
		skill  string
		cwd    string
		valid  bool
	}{
		{"valid absolute paths", "/synthetic/bin/pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree", true},
		{"relative binary", "pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree", false},
		{"relative actor", "/synthetic/bin/pi", "agents/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree", false},
		{"relative skill", "/synthetic/bin/pi", "/synthetic/actor.md", "skills/SKILL.md", "/synthetic/worktree", false},
		{"relative cwd", "/synthetic/bin/pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "worktree", false},
		{"unclean path", "/synthetic/bin/../bin/pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BindInvocationPaths(qarole.RequirementsAnalyst, tc.binary, tc.actor, tc.skill, tc.cwd)
			if (err == nil) != tc.valid {
				t.Fatalf("BindInvocationPaths() error = %v, want valid=%t", err, tc.valid)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		route   qaroute.ResolvedRoute
		binding BoundInvocationPaths
	}{
		{"unbound paths", resolvedRoute(t, qarole.RequirementsAnalyst), BoundInvocationPaths{}},
		{"invalid role route", qaroute.ResolvedRoute{Role: "unknown", Backend: "pi", Provider: "nan", Model: "qwen3.6", Effort: "high", PolicyVersion: qaroute.PolicyVersion}, valid()},
		{"invalid route model", qaroute.ResolvedRoute{Role: qarole.RequirementsAnalyst, Backend: "pi", Provider: "nan", Model: "glm5.3", Effort: "high", PolicyVersion: qaroute.PolicyVersion}, valid()},
		{"route does not match bound role", resolvedRoute(t, qarole.TestRunner), valid()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildInvocation(tc.route, tc.binding); err == nil {
				t.Fatal("BuildInvocation() succeeded")
			}
		})
	}
}

// This signature has no custom-tool parameter; the fixed token is the only API.
var _ func(qaroute.ResolvedRoute, BoundInvocationPaths) (Invocation, error) = BuildInvocation

func resolvedRoute(t *testing.T, role qarole.RoleID) qaroute.ResolvedRoute {
	t.Helper()
	route, failure := qaroute.Resolve(qaroute.Request{Role: role, Backend: "pi"}, qaroute.Snapshot{})
	if failure.Code != "" {
		t.Fatalf("Resolve() failure = %q", failure.Code)
	}
	return route
}

type goldenInvocation struct {
	binary string
	cwd    string
	argv   []string
}

func readInvocationGolden(t *testing.T, name string) goldenInvocation {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pi-0.85.1", "invocation", name))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 4 || lines[0] != "# Synthetic expected invocation; not a Pi runtime observation." || !strings.HasPrefix(lines[1], "binary=") || !strings.HasPrefix(lines[2], "cwd=") || lines[3] != "argv:" {
		t.Fatalf("invalid golden %q", name)
	}
	return goldenInvocation{binary: strings.TrimPrefix(lines[1], "binary="), cwd: strings.TrimPrefix(lines[2], "cwd="), argv: append([]string(nil), lines[4:]...)}
}
