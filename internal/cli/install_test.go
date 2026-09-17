package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"os/exec"

	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

func TestRunInstallCompatibleFreshRoot(t *testing.T) {
	home := t.TempDir()
	deps := compatibleInstallDependencies(t, home)
	calls := 0
	apply := deps.applyGroup
	deps.applyGroup = func(requests []installtxn.GroupRequest, backupRoot, backupName string) (installtxn.GroupResult, error) {
		calls++
		if len(requests) != 1 || requests[0].Plan.RuntimeID() != runtimematrix.RuntimePi || backupRoot != home || backupName != ".cortex-backup-000102030405060708090a0b0c0d0e0f" {
			t.Fatal("unexpected grouped transaction")
		}
		return apply(requests, backupRoot, backupName)
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, readyRunner(), deps); code != exitOK {
		t.Fatalf("install exit = %d, stderr = %q", code, stderr.String())
	}
	if calls != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "operation=install status=completed touch=applied") || !strings.Contains(stdout.String(), "runtime=pi presence=present compatibility=compatible action=configure touch=applied\n") {
		t.Fatalf("install output = %q, calls = %d", stdout.String(), calls)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", ".cortex", "install-state.json")); err != nil {
		t.Fatalf("install state = %v", err)
	}
	stdout.Reset()
	if code := runWithInstallDependencies(context.Background(), []string{"update"}, &stdout, &stderr, readyRunner(), deps); code != exitOK || !strings.Contains(stdout.String(), "unchanged=2") {
		t.Fatalf("idempotent update = (%d, %q)", code, stdout.String())
	}
}
func TestRunInstallUnrepresentableProjectionIsNotApplied(t *testing.T) {
	deps := compatibleInstallDependencies(t, t.TempDir())
	deps.buildRequests = func([]runtimematrix.Observation, installDependencies, bool) ([]installtxn.GroupRequest, []projection.RuntimeResult, error) {
		return nil, []projection.RuntimeResult{{ID: runtimematrix.RuntimePi, Outcome: runtimematrix.OutcomePresentCompatible, Action: runtimematrix.Skip}}, nil
	}
	deps.home = func() (string, error) { t.Fatal("home called"); return "", nil }
	deps.backupName = func() (string, error) { t.Fatal("backup name called"); return "", nil }
	deps.applyGroup = nil
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, readyRunner(), deps); code != exitUnknown {
		t.Fatalf("install exit = %d, want %d", code, exitUnknown)
	}
	want := "operation=install status=not_applied reason=projection_unrepresentable touch=denied\n" +
		"runtime=pi presence=present compatibility=compatible action=skip touch=denied\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("install = (%q, %q)", stdout.String(), stderr.String())
	}
}
func TestRunInstallFailuresDoNotReportCompletion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		apply  func(*installDependencies)
		code   int
		reason string
	}{
		{"conflict", func(deps *installDependencies) {
			deps.applyGroup = func([]installtxn.GroupRequest, string, string) (installtxn.GroupResult, error) {
				return installtxn.GroupResult{}, installtxn.ErrConflict
			}
		}, exitConflict, "ownership_conflict"},
		{"transaction", func(deps *installDependencies) {
			deps.applyGroup = func([]installtxn.GroupRequest, string, string) (installtxn.GroupResult, error) {
				return installtxn.GroupResult{}, errors.New("private")
			}
		}, exitTransaction, "transaction_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := compatibleInstallDependencies(t, t.TempDir())
			tc.apply(&deps)
			var stdout, stderr bytes.Buffer
			if code := runWithInstallDependencies(context.Background(), []string{"update"}, &stdout, &stderr, readyRunner(), deps); code != tc.code || stderr.Len() != 0 || !strings.Contains(stdout.String(), "reason="+tc.reason) || strings.Contains(stdout.String(), "status=completed") {
				t.Fatalf("update = (%d, %q, %q)", code, stdout.String(), stderr.String())
			}
		})
	}
}
func TestRunInstallCompositionFailures(t *testing.T) {
	for _, change := range []func(*installDependencies){
		func(deps *installDependencies) {
			deps.home = func() (string, error) { return "", errors.New("private") }
		},
		func(deps *installDependencies) {
			deps.backupName = func() (string, error) { return "", errors.New("private") }
		},
	} {
		deps := compatibleInstallDependencies(t, t.TempDir())
		change(&deps)
		var stdout, stderr bytes.Buffer
		if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, readyRunner(), deps); code != exitFailure || stdout.Len() != 0 || stderr.String() != "error=install_composition_failed\n" {
			t.Fatalf("composition failure = (%d, %q, %q)", code, stdout.String(), stderr.String())
		}
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("private") }

func TestRunInstallOutputFailureDoesNotLeak(t *testing.T) {
	home := t.TempDir()
	var stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, errorWriter{}, &stderr, readyRunner(), compatibleInstallDependencies(t, home)); code != exitFailure || stderr.String() != "error=output_failed\n" {
		t.Fatalf("output failure = (%d, %q)", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", ".cortex", "install-state.json")); err != nil {
		t.Fatalf("completed mutation was not materialized: %v", err)
	}
}

func compatibleInstallDependencies(t *testing.T, home string) installDependencies {
	t.Helper()
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{
		{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3"}},
		{ID: runtimematrix.RuntimeOpenCode, KnownIncompatible: []string{"2.3.4"}},
		{ID: runtimematrix.RuntimeClaudeCode},
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := defaultInstallDependencies()
	deps.policy = policy
	deps.home = func() (string, error) { return home, nil }
	deps.resolveRoot = func(plan skilldest.Plan) (skillroot.Plan, error) {
		return skillroot.Resolve(plan, skillroot.Inputs{Home: home})
	}
	deps.backupName = func() (string, error) { return ".cortex-backup-000102030405060708090a0b0c0d0e0f", nil }
	return deps
}

// uncertifiedRunner reports every runtime present with a normalized version
// that the injected policy neither certifies nor rejects.
func uncertifiedRunner() *fakeRunner {
	return &fakeRunner{runs: map[string]fakeRun{
		"/private/pi":       {execution: runtimeprobe.Execution{Stdout: []byte("9.9.9\n")}},
		"/private/opencode": {execution: runtimeprobe.Execution{Stdout: []byte("9.9.9\n")}},
		"/private/claude":   {execution: runtimeprobe.Execution{Stdout: []byte("9.9.9 (Claude Code)")}},
	}}
}

func TestRunInstallAllowUncertifiedAdmitsEveryPresentRuntime(t *testing.T) {
	home := t.TempDir()
	deps := compatibleInstallDependencies(t, home)
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install", "--allow-uncertified"}, &stdout, &stderr, uncertifiedRunner(), deps); code != exitOK {
		t.Fatalf("install exit = %d, stderr = %q, stdout = %q", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "operation=install status=completed touch=applied") {
		t.Fatalf("install output = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), " warning=uncertified_admission runtimes=3 certification=not_certified\n") {
		t.Fatalf("missing disclosure in %q", stdout.String())
	}
	for _, id := range []string{"pi", "opencode", "claude-code"} {
		if !strings.Contains(stdout.String(), "runtime="+id+" presence=present compatibility=uncertified action=configure touch=applied\n") {
			t.Fatalf("runtime %s was not admitted: %q", id, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "9.9.9") {
		t.Fatalf("output leaks the observed version: %q", stdout.String())
	}
}

func TestRunInstallWithoutOptInRefusesAndNamesTheOptIn(t *testing.T) {
	deps := compatibleInstallDependencies(t, t.TempDir())
	deps.buildRequests = func([]runtimematrix.Observation, installDependencies, bool) ([]installtxn.GroupRequest, []projection.RuntimeResult, error) {
		t.Fatal("build called")
		return nil, nil, nil
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, uncertifiedRunner(), deps); code != exitUnknown {
		t.Fatalf("install exit = %d, want %d", code, exitUnknown)
	}
	if !strings.HasPrefix(stdout.String(), "operation=install status=not_applied reason=compatibility_uncertified touch=denied opt_in=--allow-uncertified\n") || stderr.Len() != 0 {
		t.Fatalf("install = (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestRunInstallRefusesWithoutAdvertisingAnUnhelpfulOptIn(t *testing.T) {
	deps := compatibleInstallDependencies(t, t.TempDir())
	// opencode 2.3.4 is known-incompatible, claude reports no parseable
	// version, and pi is absent: the opt-in would admit nothing.
	runner := &fakeRunner{
		lookup: map[string]error{"pi": exec.ErrNotFound},
		runs: map[string]fakeRun{
			"/private/opencode": {execution: runtimeprobe.Execution{Stdout: []byte("2.3.4\n")}},
			"/private/claude":   {execution: runtimeprobe.Execution{Stdout: []byte("not-a-version")}},
		},
	}
	for _, args := range [][]string{{"install"}, {"install", "--allow-uncertified"}} {
		var stdout, stderr bytes.Buffer
		if code := runWithInstallDependencies(context.Background(), args, &stdout, &stderr, runner, deps); code != exitUnknown {
			t.Fatalf("%v exit = %d, want %d", args, code, exitUnknown)
		}
		if !strings.HasPrefix(stdout.String(), "operation=install status=not_applied reason=compatibility_uncertified touch=denied\n") || stderr.Len() != 0 {
			t.Fatalf("%v = (%q, %q)", args, stdout.String(), stderr.String())
		}
	}
}

func TestRunInstallAllowUncertifiedStillRefusesUnrepresentableProjection(t *testing.T) {
	deps := compatibleInstallDependencies(t, t.TempDir())
	deps.buildRequests = func(_ []runtimematrix.Observation, _ installDependencies, allowUncertified bool) ([]installtxn.GroupRequest, []projection.RuntimeResult, error) {
		if !allowUncertified {
			t.Fatal("opt-in was not threaded to the build seam")
		}
		return nil, []projection.RuntimeResult{{ID: runtimematrix.RuntimePi, Outcome: runtimematrix.OutcomePresentUncertified, Action: runtimematrix.Skip}}, nil
	}
	deps.home = func() (string, error) { t.Fatal("home called"); return "", nil }
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install", "--allow-uncertified"}, &stdout, &stderr, uncertifiedRunner(), deps); code != exitUnknown {
		t.Fatalf("install exit = %d, want %d", code, exitUnknown)
	}
	want := "operation=install status=not_applied reason=projection_unrepresentable touch=denied\n" +
		"runtime=pi presence=present compatibility=uncertified action=skip touch=denied\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("install = (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestRunInstallAllowUncertifiedSkipsOnlyTheKnownIncompatibleRuntime(t *testing.T) {
	home := t.TempDir()
	deps := compatibleInstallDependencies(t, home)
	runner := &fakeRunner{
		lookup: map[string]error{"claude": exec.ErrNotFound},
		runs: map[string]fakeRun{
			"/private/pi":       {execution: runtimeprobe.Execution{Stdout: []byte("9.9.9\n")}},
			"/private/opencode": {execution: runtimeprobe.Execution{Stdout: []byte("2.3.4\n")}},
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install", "--allow-uncertified"}, &stdout, &stderr, runner, deps); code != exitOK {
		t.Fatalf("install exit = %d, stderr = %q, stdout = %q", code, stderr.String(), stdout.String())
	}
	want := "runtime=pi presence=present compatibility=uncertified action=configure touch=applied\n" +
		"runtime=opencode presence=present compatibility=incompatible action=skip touch=denied\n" +
		"runtime=claude-code presence=absent action=warn touch=denied\n"
	if !strings.HasSuffix(stdout.String(), want) {
		t.Fatalf("install output = %q, want suffix %q", stdout.String(), want)
	}
	if !strings.Contains(stdout.String(), " warning=uncertified_admission runtimes=1 certification=not_certified\n") {
		t.Fatalf("missing disclosure in %q", stdout.String())
	}
}

func TestRunInstallRejectsUnknownAndRepeatedFlags(t *testing.T) {
	for _, args := range [][]string{
		{"install", "--allow-uncertified", "--allow-uncertified"},
		{"install", "--allow-uncertified", "extra"},
		{"install", "--allow-uncertified=true"},
		{"update", "--allow-uncertified"},
	} {
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), args, &stdout, &stderr, readyRunner()); code != exitUsage {
			t.Fatalf("%v exit = %d, want %d", args, code, exitUsage)
		}
		if stdout.Len() != 0 || stderr.String() != "error=invalid_command\n" {
			t.Fatalf("%v output = (%q, %q)", args, stdout.String(), stderr.String())
		}
	}
}
