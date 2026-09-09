package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
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
	deps.buildRequests = func([]runtimematrix.Observation, installDependencies) ([]installtxn.GroupRequest, []projection.RuntimeResult, error) {
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
