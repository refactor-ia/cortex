package qapi

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qagit"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

func TestPreflightBindingHandsOffBoundAssetsRouteAndGit(t *testing.T) {
	fixture := newPreflightFixture(t)
	got, err := preflightBinding(context.Background(), fixture.request, filepath.Join(t.TempDir(), "missing"), fixture.installRoot, fixture.snapshot, fixture.runner)
	if err != nil {
		t.Fatal(err)
	}
	if got.route.Model != "glm5.2" || got.route.ProfileID != "role-default" || got.assets.RoleID() != fixture.request.Role || got.assets.ActorSHA256() != fixture.expected.ActorSHA256 || got.assets.SkillSHA256() != fixture.expected.SkillSHA256 || got.git.Revision != fixture.request.Revision || got.git.Fingerprint != fixture.request.Fingerprint {
		t.Fatalf("preflightBinding() = %#v", got)
	}
	if len(fixture.runner.calls) != 6 {
		t.Fatalf("Git calls = %d, want 6", len(fixture.runner.calls))
	}
}

func TestPreflightBindingStopsAtAssetsBeforeGit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot func(preflightFixture) catalog.CatalogSnapshot
		change   func(t *testing.T, fixture preflightFixture)
	}{
		{name: "catalog binding failure", snapshot: func(preflightFixture) catalog.CatalogSnapshot { return catalog.CatalogSnapshot{} }},
		{name: "wrong role asset", snapshot: func(fixture preflightFixture) catalog.CatalogSnapshot { return fixture.snapshot }, change: func(t *testing.T, fixture preflightFixture) {
			wrong := selectedActor(t, productionActorBinding(t, fixture.snapshot), qarole.TestDesigner)
			if err := os.WriteFile(fixture.actorPath, wrong.Content(), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			if tc.change != nil {
				tc.change(t, fixture)
			}
			_, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, tc.snapshot(fixture), fixture.runner)
			assertIdentityFailure(t, err, "assets")
			if len(fixture.runner.calls) != 0 {
				t.Fatalf("Git calls = %d, want 0", len(fixture.runner.calls))
			}
		})
	}
}

func TestPreflightBindingStopsAtRouteBeforeGit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		malformed bool
	}{
		{name: "missing explicit profile"},
		{name: "malformed explicit profile", malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			fixture.request.Profile = "balanced"
			if tc.malformed {
				writeProfile(t, fixture.profileRoot, []byte("{"))
			}
			_, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			assertIdentityFailure(t, err, "route")
			if len(fixture.runner.calls) != 0 {
				t.Fatalf("Git calls = %d, want 0", len(fixture.runner.calls))
			}
		})
	}
}

func TestPreflightBindingStopsAtCleanBinding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results func(preflightFixture) []qagit.Result
	}{
		{name: "dirty", results: func(fixture preflightFixture) []qagit.Result {
			return preflightGitResults(fixture.request.CurrentDirectory, fixture.request.Revision, strings.Repeat("b", 40), "? new\x00")
		}},
		{name: "stale", results: func(fixture preflightFixture) []qagit.Result {
			return preflightGitResults(fixture.request.CurrentDirectory, strings.Repeat("c", 40), strings.Repeat("b", 40), "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			fixture.runner.results = tc.results(fixture)
			_, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			assertIdentityFailure(t, err, "clean_binding")
			if len(fixture.runner.calls) == 0 {
				t.Fatal("Git was not called")
			}
		})
	}
}

func TestPreflightBindingRejectsInvalidRequestBeforeObservationOrGit(t *testing.T) {
	fixture := newPreflightFixture(t)
	fixture.request.Backend = "other"
	_, err := preflightBinding(context.Background(), fixture.request, filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "missing"), catalog.CatalogSnapshot{}, fixture.runner)
	if !errors.Is(err, errInvalidAdmissionRequest) || len(fixture.runner.calls) != 0 {
		t.Fatalf("preflightBinding() = %v, Git calls = %d", err, len(fixture.runner.calls))
	}
}

type preflightFixture struct {
	request                             AdmissionRequest
	snapshot                            catalog.CatalogSnapshot
	expected                            installobserve.AdmissionBinding
	installRoot, profileRoot, actorPath string
	runner                              *preflightGitRunner
}

func newPreflightFixture(t *testing.T) preflightFixture {
	t.Helper()
	snapshot := productionCatalogSnapshot(t)
	actors := productionActorBinding(t, snapshot)
	neutral, err := skillrender.Render(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	skills, err := skillprojection.Build(runtimematrix.RuntimePi, neutral)
	if err != nil {
		t.Fatal(err)
	}
	role := qarole.ExploratoryTester
	expected, err := CatalogAdmissionBinding(snapshot, role, "pi")
	if err != nil {
		t.Fatal(err)
	}
	root, cwd := testProfileRoot(t), testProfileRoot(t)
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	inputs := []installstate.V2ArtifactInput{{LogicalID: "skills/" + string(role), Kind: installstate.KindSkill, CapabilityID: string(role), RelativePath: "skills/cortex-" + string(role) + "/SKILL.md", SHA256: expected.SkillSHA256, InstallationID: "000102030405060708090a0b0c0d0e0f"}}
	for _, actor := range actors.Actors() {
		inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: actor.LogicalID(), Kind: installstate.KindPiActor, RoleID: actor.RoleID(), ActorContractVersion: qaactor.ActorContractVersion, RelativePath: "agents/" + actor.Name() + ".md", SHA256: actor.GeneratedSHA256(), InstallationID: "000102030405060708090a0b0c0d0e0f"})
	}
	state, err := installstate.NewV2(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, snapshot.Fingerprint(), "000102030405060708090a0b0c0d0e0f", inputs)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := installstate.Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	actor, skill := selectedActor(t, actors, role), selectedSkill(t, skills, role)
	actorPath := filepath.Join(root, "agents", "cortex-"+string(role)+".md")
	writePreflightFile(t, filepath.Join(root, ".cortex", "install-state.json"), encoded)
	writePreflightFile(t, actorPath, actor.Content())
	writePreflightFile(t, filepath.Join(root, "skills", "cortex-"+string(role), "SKILL.md"), skill.Content())
	request := AdmissionRequest{Role: role, Backend: "pi", CurrentDirectory: cwd, Revision: head, Fingerprint: preflightFingerprint("sha1", head, tree), Task: "inspect the candidate", TimeoutSeconds: 900}
	return preflightFixture{request: request, snapshot: snapshot, expected: expected, installRoot: root, profileRoot: testProfileRoot(t), actorPath: actorPath, runner: &preflightGitRunner{results: preflightGitResults(cwd, head, tree, "")}}
}

func writePreflightFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type preflightGitRunner struct {
	results []qagit.Result
	calls   []qagit.Command
}

func (runner *preflightGitRunner) Run(_ context.Context, command qagit.Command) (qagit.Result, error) {
	runner.calls = append(runner.calls, command)
	return runner.results[len(runner.calls)-1], nil
}

func preflightGitResults(cwd, head, tree, status string) []qagit.Result {
	return []qagit.Result{{Stdout: []byte(cwd + "\n")}, {Stdout: []byte(cwd + "\n")}, {Stdout: []byte("sha1\n")}, {Stdout: []byte(head + "\n")}, {Stdout: []byte(tree + "\n")}, {Stdout: []byte(status)}}
}

func preflightFingerprint(format, revision, tree string) string {
	hash := sha256.New()
	for _, value := range []string{"cortex.qa.clean-candidate.v1", format, revision, tree} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("candidate.%x", hash.Sum(nil))
}

func assertIdentityFailure(t *testing.T, err error, stage string) {
	t.Helper()
	var failure *IdentityInsufficientError
	if !errors.As(err, &failure) || errors.Unwrap(failure) == nil || failure.Stage != stage || failure.Error() != "admission identity is insufficient" {
		t.Fatalf("preflight failure = %v", err)
	}
}
