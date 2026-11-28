package qapi

import (
	"fmt"
	"os"
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
		{"invalid role route", qaroute.ResolvedRoute{Role: "unknown", Backend: "pi", Provider: "example-provider", Model: "example-model", Effort: "high", PolicyVersion: qaroute.PolicyVersion}, valid()},
		{"invalid route model", qaroute.ResolvedRoute{Role: qarole.RequirementsAnalyst, Backend: "pi", Provider: "example-provider", Model: "example-model-b", Effort: "high", PolicyVersion: qaroute.PolicyVersion}, valid()},
		{"route does not match bound role", resolvedRoute(t, qarole.TestRunner), valid()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := BuildInvocation(tc.route, tc.binding); err == nil {
				t.Fatal("BuildInvocation() succeeded")
			}
		})
	}
}

// D1 (#458): with no profile, a complete provider/model/effort override is the
// only role-default route, and it still builds an invocation; a partial one
// carries no default to fill the rest.
func TestBuildInvocationAcceptsAFullOverrideRoleDefaultRoute(t *testing.T) {
	binding, err := BindInvocationPaths(qarole.TestRunner, "/synthetic/bin/pi", "/synthetic/actor.md", "/synthetic/skill/SKILL.md", "/synthetic/worktree")
	if err != nil {
		t.Fatal(err)
	}
	route, failure := qaroute.Resolve(qaroute.Request{Role: qarole.TestRunner, Backend: "pi", Override: qaroute.Override{Provider: "openai-codex", Model: "gpt-5.6", Effort: "low"}}, qaroute.Snapshot{})
	if failure.Code != "" {
		t.Fatalf("Resolve() failure = %q", failure.Code)
	}
	invocation, err := BuildInvocation(route, binding)
	if err != nil {
		t.Fatalf("BuildInvocation() = %v", err)
	}
	if got := strings.Join(invocation.Argv()[:2], " "); got != "--model openai-codex/gpt-5.6" {
		t.Fatalf("argv = %v", invocation.Argv())
	}
	partial := route
	partial.OverrideFields = []string{"provider", "model"}
	if _, err := BuildInvocation(partial, binding); err == nil {
		t.Fatal("BuildInvocation() accepted a role-default route without a full override")
	}
}

// fixtureRoutes are the per-role routes the qapi fixtures and goldens were
// written against. Roles carry no default route (D1, #458), so fixtures reach
// them only through an owner profile.
var fixtureRoutes = map[qarole.RoleID][3]string{
	qarole.RequirementsAnalyst: {"example-provider", "example-model", "high"},
	qarole.TestDesigner:        {"example-provider", "example-model", "high"},
	qarole.ExploratoryTester:   {"example-provider", "example-model-b", "high"},
	qarole.AdversarialTester:   {"example-provider", "example-model-c", "high"},
	qarole.TestRunner:          {"example-provider", "example-model", "low"},
	qarole.EvidenceAuditor:     {"example-provider", "example-model-b", "high"},
}

// fixtureResolve resolves one role's fixture route through a profile snapshot.
func fixtureResolve(role qarole.RoleID, backend string) (qaroute.ResolvedRoute, qaroute.Failure) {
	route := fixtureRoutes[role]
	snapshot := fmt.Sprintf(`{"schemaVersion":1,"defaultProfile":"fixture","profiles":{"fixture":{"routes":{%q:{%q:{"provider":%q,"model":%q,"effort":%q}}}}}}`, role, backend, route[0], route[1], route[2])
	return qaroute.Resolve(qaroute.Request{Role: role, Backend: backend, ProfileID: "fixture"}, qaroute.Snapshot{Present: true, Bytes: []byte(snapshot)})
}

// writeFixtureProfile stores one role's fixture route as the catalog root's
// default profile.
func writeFixtureProfile(t *testing.T, root string, role qarole.RoleID, backend string) {
	t.Helper()
	route := fixtureRoutes[role]
	writeProfile(t, root, []byte(fmt.Sprintf(`{"schemaVersion":1,"defaultProfile":"fixture","profiles":{"fixture":{"routes":{%q:{%q:{"provider":%q,"model":%q,"effort":%q}}}}}}`, role, backend, route[0], route[1], route[2])))
}

// This signature has no custom-tool parameter; the fixed token is the only API.
var _ func(qaroute.ResolvedRoute, BoundInvocationPaths) (Invocation, error) = BuildInvocation

func resolvedRoute(t *testing.T, role qarole.RoleID) qaroute.ResolvedRoute {
	t.Helper()
	route, failure := fixtureResolve(role, "pi")
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
	data, err := os.ReadFile(backendTestdata(piFixtureBackend, piFixtureVersion, "invocation", name))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 4 || lines[0] != "# Synthetic expected invocation; not a Pi runtime observation." || !strings.HasPrefix(lines[1], "binary=") || !strings.HasPrefix(lines[2], "cwd=") || lines[3] != "argv:" {
		t.Fatalf("invalid golden %q", name)
	}
	return goldenInvocation{binary: strings.TrimPrefix(lines[1], "binary="), cwd: strings.TrimPrefix(lines[2], "cwd="), argv: append([]string(nil), lines[4:]...)}
}

// TestBindInvocationPathsAcceptsNoInstalledActor covers the runtimes that
// never receive an actor file: their binding carries no actor path, and the
// skill path stays mandatory because they do load that from disk.
func TestBindInvocationPathsAcceptsNoInstalledActor(t *testing.T) {
	binding, err := BindInvocationPaths(qarole.RequirementsAnalyst, "/bin/claude", "", "/root/skills/cortex-requirements-analyst/SKILL.md", "/work")
	if err != nil {
		t.Fatalf("BindInvocationPaths() error = %v", err)
	}
	if binding.actor != "" {
		t.Fatalf("actor = %q, want empty", binding.actor)
	}
	if _, err := BindInvocationPaths(qarole.RequirementsAnalyst, "/bin/claude", "", "", "/work"); err == nil {
		t.Fatal("BindInvocationPaths() accepted an empty skill path")
	}
}

// TestPiInvocationStillRequiresTheActorPath pins that Pi, whose argv carries
// the actor file, cannot be built from a binding that has none.
func TestPiInvocationStillRequiresTheActorPath(t *testing.T) {
	route, failure := fixtureResolve(qarole.RequirementsAnalyst, "pi")
	if failure.Code != "" {
		t.Fatalf("route failure = %q", failure.Code)
	}
	binding, err := BindInvocationPaths(qarole.RequirementsAnalyst, "/bin/pi", "", "/root/skills/cortex-requirements-analyst/SKILL.md", "/work")
	if err != nil {
		t.Fatalf("BindInvocationPaths() error = %v", err)
	}
	if _, err := BuildInvocation(route, binding); err == nil {
		t.Fatal("BuildInvocation() accepted a Pi binding with no actor path")
	}
}
