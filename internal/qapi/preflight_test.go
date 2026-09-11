package qapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qagit"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
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
	if got.route.Model != "glm5.2" || got.route.ProfileID != "role-default" || got.assets.RoleID() != fixture.request.Role || got.assets.ActorSHA256() != fixture.expected.ActorSHA256 || got.assets.ActorSourceSHA256() != fixture.expected.ActorSourceSHA256 || got.assets.ActorBindingSHA256() != fixture.expected.ActorBindingSHA256 || got.assets.SkillSHA256() != fixture.expected.SkillSHA256 || got.git.Revision != fixture.request.Revision || got.git.Fingerprint != fixture.request.Fingerprint {
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

func TestPrelaunchReceiptBasisMapsPreflightIdentityWithoutTerminalFacts(t *testing.T) {
	fixture := newPreflightFixture(t)
	fixture.request.Override = qaroute.Override{Provider: "nan", Model: "glm5.2", Effort: "high"}
	flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
	if err != nil {
		t.Fatal(err)
	}
	pi := syntheticBoundPi()
	wantBounds, ok := qaadmission.BoundsForTimeout(fixture.request.TimeoutSeconds)
	if !ok {
		t.Fatal("fixture timeout is invalid")
	}
	got, code, err := prelaunchReceiptBasis(fixture.request, flight, pi)
	if err != nil || code != qaadmission.CodeAdmitted {
		t.Fatalf("prelaunchReceiptBasis() code=%q, err=%v", code, err)
	}
	if got.Contract != qaadmission.Contract || got.Role != fixture.request.Role || got.Backend != fixture.request.Backend ||
		got.Versions != (qaadmission.Versions{Receipt: qaadmission.Contract, Policy: flight.route.PolicyVersion, Profile: qaroute.ProfileContract, Adapter: "cortex.qa.pi-admission.v1", ActorContract: qaactor.ActorContractVersion, SkillContract: skillContract, InputContract: InputContract, ProbeContract: ProbeContract, Runtime: pi.version}) ||
		got.Installation.ID != flight.assets.InstallationID() || got.Installation.CatalogSHA256 != flight.assets.CatalogFingerprint() || got.Installation.ActorSourceSHA256 != flight.assets.ActorSourceSHA256() || got.Installation.ActorGeneratedSHA256 != flight.assets.ActorSHA256() || got.Installation.ActorBindingSHA256 != flight.assets.ActorBindingSHA256() || got.Installation.SkillGeneratedSHA256 != flight.assets.SkillSHA256() ||
		got.Target != (qaadmission.TargetIdentity{CWDIdentity: flight.git.CWDIdentity, Revision: flight.git.Revision, Tree: flight.git.Tree, Fingerprint: flight.git.Fingerprint}) || got.Binary.Contract != qaadmission.BinaryContract || got.Binary.SHA256 != fmt.Sprintf("%x", pi.digest) || got.Binary.SizeBytes != pi.size ||
		got.Route.Requested != (qaadmission.RequestedIdentity{Provider: fixture.request.Override.Provider, Model: fixture.request.Override.Model, Effort: fixture.request.Override.Effort}) || !reflect.DeepEqual(got.Route.Resolved, flight.route) || got.Route.Observed != (qaadmission.ObservedIdentity{Effort: qaadmission.UnobservableEffort()}) || got.Bounds != wantBounds {
		t.Fatalf("prelaunchReceiptBasis() provenance = %#v", got)
	}
	if got.ReceiptID != "" || got.Status != "" || got.Code != "" || got.AttemptedRun || got.Availability != (qaadmission.AvailabilityFacts{}) || got.Execution != (qaadmission.ExecutionFacts{}) || got.Diagnostic != nil {
		t.Fatalf("prelaunchReceiptBasis() invented terminal facts: %#v", got)
	}
	minimum, err := qaadmission.MinimumSize(got)
	if err != nil || !qaadmission.BoundsSatisfiable(got, minimum) || qaadmission.PrelaunchCode(got, minimum) != qaadmission.CodeAdmitted {
		t.Fatalf("prelaunch basis minimum=%d, err=%v", minimum, err)
	}
}

func TestPrelaunchReceiptBasisUsesRequestedTimeoutBounds(t *testing.T) {
	for _, timeout := range []int{qaadmission.MinimumTimeoutSeconds, qaadmission.MaximumTimeoutSeconds} {
		t.Run(fmt.Sprintf("timeout %d", timeout), func(t *testing.T) {
			fixture := newPreflightFixture(t)
			fixture.request.TimeoutSeconds = timeout
			flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			if err != nil {
				t.Fatal(err)
			}
			got, code, err := prelaunchReceiptBasis(fixture.request, flight, syntheticBoundPi())
			wantBounds, ok := qaadmission.BoundsForTimeout(timeout)
			minimum, sizeErr := qaadmission.MinimumSize(got)
			if err != nil || !ok || sizeErr != nil || code != qaadmission.CodeAdmitted || got.Bounds != wantBounds || !qaadmission.BoundsSatisfiable(got, minimum) {
				t.Fatalf("prelaunchReceiptBasis() = %#v, %q, %v; minimum=%d, %v", got, code, err, minimum, sizeErr)
			}
		})
	}
}

func TestPrelaunchReceiptBasisRejectsIncompleteBinaryIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*boundPi)
	}{
		{"version", func(pi *boundPi) { pi.version = "" }},
		{"digest", func(pi *boundPi) { pi.digest = [sha256.Size]byte{} }},
		{"size", func(pi *boundPi) { pi.size = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			if err != nil {
				t.Fatal(err)
			}
			pi := syntheticBoundPi()
			tc.mutate(&pi)
			got, code, err := prelaunchReceiptBasis(fixture.request, flight, pi)
			var failure *IdentityInsufficientError
			if !reflect.DeepEqual(got, qaadmission.Receipt{}) || code != "" || !errors.As(err, &failure) || failure.Stage != "binary" {
				t.Fatalf("prelaunchReceiptBasis() = %#v, %q, %v", got, code, err)
			}
		})
	}
}

func syntheticBoundPi(cwd ...string) boundPi {
	var digest [sha256.Size]byte
	for index := range digest {
		digest[index] = byte(index + 1)
	}
	pi := boundPi{version: RuntimeVersion, digest: digest, size: 1}
	if len(cwd) == 1 {
		pi.cwd = cwd[0]
	}
	return pi
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

func TestPrelaunchAvailabilityRequiresBoundPiCanonicalRequestDirectory(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(t *testing.T, fixture *preflightFixture, pi *boundPi)
		stage     string
		probes    int
	}{
		{name: "matching canonical directory", configure: func(_ *testing.T, fixture *preflightFixture, pi *boundPi) {
			pi.cwd = fixture.request.CurrentDirectory
		}, probes: 1},
		{name: "different existing canonical directory", configure: func(t *testing.T, _ *preflightFixture, pi *boundPi) {
			pi.cwd = testProfileRoot(t)
		}, stage: "pi"},
		{name: "empty bound directory", configure: func(_ *testing.T, _ *preflightFixture, pi *boundPi) {
			pi.cwd = ""
		}, stage: "pi"},
		{name: "noncanonical bound directory", configure: func(_ *testing.T, fixture *preflightFixture, pi *boundPi) {
			pi.cwd = fixture.request.CurrentDirectory + "/."
		}, stage: "pi"},
		{name: "uncanonicalizable request directory", configure: func(t *testing.T, fixture *preflightFixture, pi *boundPi) {
			fixture.request.CurrentDirectory = filepath.Join(t.TempDir(), "missing")
			pi.cwd = fixture.request.CurrentDirectory
		}, stage: "pi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			if err != nil {
				t.Fatal(err)
			}
			fixture.runner.calls = nil
			probes := 0
			ops := readyPrelaunchOps(&probes, availabilityProbes{model: ModelProbeResult{Available: true}, auth: AuthProbeResult{Ready: true}})
			pi := syntheticBoundPi(fixture.request.CurrentDirectory)
			tc.configure(t, &fixture, &pi)

			got, code, err := prelaunchAvailability(context.Background(), fixture.request, flight, pi, fixture.installRoot, fixture.snapshot, fixture.runner, &ops)
			if tc.stage == "" {
				if err != nil || code != qaadmission.CodeAdmitted || probes != tc.probes {
					t.Fatalf("prelaunchAvailability() = %#v, %q, %v, probes=%d", got, code, err, probes)
				}
				return
			}
			if !reflect.DeepEqual(got, qaadmission.Receipt{}) || code != "" || probes != tc.probes {
				t.Fatalf("prelaunchAvailability() = %#v, %q, %v, probes=%d", got, code, err, probes)
			}
			assertIdentityFailure(t, err, tc.stage)
		})
	}
}

func TestPrelaunchAvailabilityFailsClosedBeforeProbes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(t *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string)
		stage     string
	}{
		{name: "invalid request", configure: func(_ *testing.T, fixture preflightFixture, _ preflight, _ *prelaunchOps) (AdmissionRequest, string) {
			fixture.request.Backend = "other"
			return fixture.request, fixture.installRoot
		}, stage: "request"},
		{name: "Pi revalidation failure", configure: func(_ *testing.T, fixture preflightFixture, _ preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.revalidatePi = func(boundPi) error { return errors.New("changed") }
			return fixture.request, fixture.installRoot
		}, stage: "pi"},
		{name: "catalog binding failure", configure: func(_ *testing.T, fixture preflightFixture, _ preflight, _ *prelaunchOps) (AdmissionRequest, string) {
			return fixture.request, fixture.installRoot
		}, stage: "assets"},
		{name: "asset observation failure", configure: func(t *testing.T, fixture preflightFixture, _ preflight, _ *prelaunchOps) (AdmissionRequest, string) {
			if err := os.WriteFile(fixture.actorPath, []byte("replaced"), 0o600); err != nil {
				t.Fatal(err)
			}
			return fixture.request, fixture.installRoot
		}, stage: "assets"},
		{name: "installation ID replacement", configure: func(t *testing.T, fixture preflightFixture, _ preflight, _ *prelaunchOps) (AdmissionRequest, string) {
			replacePreflightInstallationID(t, fixture)
			return fixture.request, fixture.installRoot
		}, stage: "assets"},
		{name: "asset path replacement", configure: func(t *testing.T, fixture preflightFixture, _ preflight, _ *prelaunchOps) (AdmissionRequest, string) {
			return fixture.request, newPreflightFixture(t).installRoot
		}, stage: "assets"},
		{name: "different Git CWD", configure: func(_ *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.verifyCleanBinding = func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error) {
				binding := flight.git
				binding.CWDIdentity = "cwd." + strings.Repeat("c", 64)
				return binding, nil
			}
			return fixture.request, fixture.installRoot
		}, stage: "clean_binding"},
		{name: "different Git tree", configure: func(_ *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.verifyCleanBinding = func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error) {
				binding := flight.git
				binding.Tree = strings.Repeat("c", 40)
				return binding, nil
			}
			return fixture.request, fixture.installRoot
		}, stage: "clean_binding"},
		{name: "different Git revision", configure: func(_ *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.verifyCleanBinding = func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error) {
				binding := flight.git
				binding.Revision = strings.Repeat("c", 40)
				return binding, nil
			}
			return fixture.request, fixture.installRoot
		}, stage: "clean_binding"},
		{name: "different Git fingerprint", configure: func(_ *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.verifyCleanBinding = func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error) {
				binding := flight.git
				binding.Fingerprint = "candidate." + strings.Repeat("c", 64)
				return binding, nil
			}
			return fixture.request, fixture.installRoot
		}, stage: "clean_binding"},
		{name: "different Git object format", configure: func(_ *testing.T, fixture preflightFixture, flight preflight, ops *prelaunchOps) (AdmissionRequest, string) {
			ops.verifyCleanBinding = func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error) {
				binding := flight.git
				binding.ObjectFormat = "sha256"
				return binding, nil
			}
			return fixture.request, fixture.installRoot
		}, stage: "clean_binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			if err != nil {
				t.Fatal(err)
			}
			fixture.runner.calls = nil
			probes := 0
			ops := readyPrelaunchOps(&probes, availabilityProbes{model: ModelProbeResult{Available: true}, auth: AuthProbeResult{Ready: true}})
			request, installRoot := tc.configure(t, fixture, flight, &ops)
			snapshot := fixture.snapshot
			if tc.name == "catalog binding failure" {
				snapshot = catalog.CatalogSnapshot{}
			}
			got, code, err := prelaunchAvailability(context.Background(), request, flight, syntheticBoundPi(request.CurrentDirectory), installRoot, snapshot, fixture.runner, &ops)
			if !reflect.DeepEqual(got, qaadmission.Receipt{}) || code != "" || probes != 0 {
				t.Fatalf("prelaunchAvailability() = %#v, %q, probes=%d", got, code, probes)
			}
			assertIdentityFailure(t, err, tc.stage)
		})
	}
}

func TestPrelaunchAvailabilityRejectsMissingOriginalGitObjectFormatBeforeProbes(t *testing.T) {
	fixture := newPreflightFixture(t)
	flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
	if err != nil {
		t.Fatal(err)
	}
	flight.git.ObjectFormat = ""
	fixture.runner.calls = nil
	probes := 0
	ops := readyPrelaunchOps(&probes, availabilityProbes{model: ModelProbeResult{Available: true}, auth: AuthProbeResult{Ready: true}})
	got, code, err := prelaunchAvailability(context.Background(), fixture.request, flight, syntheticBoundPi(fixture.request.CurrentDirectory), fixture.installRoot, fixture.snapshot, fixture.runner, &ops)
	if !reflect.DeepEqual(got, qaadmission.Receipt{}) || code != "" || probes != 0 {
		t.Fatalf("prelaunchAvailability() = %#v, %q, probes=%d", got, code, probes)
	}
	assertIdentityFailure(t, err, "clean_binding")
}

func TestPrelaunchAvailabilitySizesBeforeProbes(t *testing.T) {
	fixture := newPreflightFixture(t)
	flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
	if err != nil {
		t.Fatal(err)
	}
	fixture.runner.calls = nil
	probes := 0
	ops := readyPrelaunchOps(&probes, availabilityProbes{})
	ops.prelaunchReceiptBasis = func(request AdmissionRequest, flight preflight, pi boundPi) (qaadmission.Receipt, qaadmission.Code, error) {
		basis, _, err := prelaunchReceiptBasis(request, flight, pi)
		basis.Bounds.ReceiptBytes = 1
		return basis, qaadmission.CodeReceiptBoundUnsatisfiable, err
	}
	got, code, err := prelaunchAvailability(context.Background(), fixture.request, flight, syntheticBoundPi(fixture.request.CurrentDirectory), fixture.installRoot, fixture.snapshot, fixture.runner, &ops)
	if err != nil || code != qaadmission.CodeReceiptBoundUnsatisfiable || probes != 0 || got.Bounds.ReceiptBytes != 1 || got.Status != "" || got.Code != "" {
		t.Fatalf("prelaunchAvailability() = %#v, %q, %v, probes=%d", got, code, err, probes)
	}
}

func TestPrelaunchAvailabilityReturnsUnterminatedAvailabilityCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		probes availabilityProbes
		code   qaadmission.Code
	}{
		{name: "model failure wins over auth", probes: availabilityProbes{model: ModelProbeResult{Code: qaadmission.CodeModelUnavailable}, auth: AuthProbeResult{Code: qaadmission.CodeAuthNotReady}}, code: qaadmission.CodeModelUnavailable},
		{name: "auth failure", probes: availabilityProbes{model: ModelProbeResult{Available: true}, auth: AuthProbeResult{Code: qaadmission.CodeAuthNotReady}}, code: qaadmission.CodeAuthNotReady},
		{name: "both pass", probes: availabilityProbes{model: ModelProbeResult{Available: true}, auth: AuthProbeResult{Ready: true}}, code: qaadmission.CodeAdmitted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPreflightFixture(t)
			flight, err := preflightBinding(context.Background(), fixture.request, fixture.profileRoot, fixture.installRoot, fixture.snapshot, fixture.runner)
			if err != nil {
				t.Fatal(err)
			}
			fixture.runner.calls = nil
			probes := 0
			ops := readyPrelaunchOps(&probes, tc.probes)
			got, code, err := prelaunchAvailability(context.Background(), fixture.request, flight, syntheticBoundPi(fixture.request.CurrentDirectory), fixture.installRoot, fixture.snapshot, fixture.runner, &ops)
			if err != nil || code != tc.code || probes != 1 || got.Status != "" || got.Code != "" || got.ReceiptID != "" || got.AttemptedRun || got.Availability != (qaadmission.AvailabilityFacts{}) || got.Execution != (qaadmission.ExecutionFacts{}) {
				t.Fatalf("prelaunchAvailability() = %#v, %q, %v, probes=%d", got, code, err, probes)
			}
		})
	}
}

func readyPrelaunchOps(probeCalls *int, observed availabilityProbes) prelaunchOps {
	ops := defaultPrelaunchOps()
	ops.revalidatePi = func(boundPi) error { return nil }
	ops.probeAvailability = func(context.Context, boundPi, string, string) availabilityProbes {
		*probeCalls++
		return observed
	}
	return ops
}

func replacePreflightInstallationID(t *testing.T, fixture preflightFixture) {
	t.Helper()
	path := filepath.Join(fixture.installRoot, ".cortex", "install-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := bytes.ReplaceAll(data, []byte("000102030405060708090a0b0c0d0e0f"), []byte("f0e0d0c0b0a090807060504030201000"))
	if bytes.Equal(data, replaced) {
		t.Fatal("installation ID was not present")
	}
	if err := os.WriteFile(path, replaced, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertIdentityFailure(t *testing.T, err error, stage string) {
	t.Helper()
	var failure *IdentityInsufficientError
	if !errors.As(err, &failure) || errors.Unwrap(failure) == nil || failure.Stage != stage || failure.Error() != "admission identity is insufficient" {
		t.Fatalf("preflight failure = %v", err)
	}
}
