package runtimecompat_test

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
)

type fakeRun struct {
	execution runtimeprobe.Execution
	err       error
}

type fakeRunner struct {
	lookup map[string]error
	runs   map[string]fakeRun
}

func (f fakeRunner) Lookup(name string) (string, error) {
	if err := f.lookup[name]; err != nil {
		return "", err
	}
	return "/" + name, nil
}

func (f fakeRunner) Run(_ context.Context, path string, _ []string, _ []string) (runtimeprobe.Execution, error) {
	run := f.runs[path]
	return run.execution, run.err
}

func reports(t *testing.T, pi, openCode, claude fakeRun, absent ...string) []runtimeprobe.Report {
	t.Helper()
	lookup := map[string]error{}
	for _, name := range absent {
		lookup[name] = exec.ErrNotFound
	}
	reports, err := runtimeprobe.ProbeAll(context.Background(), fakeRunner{
		lookup: lookup,
		runs: map[string]fakeRun{
			"/pi":       pi,
			"/opencode": openCode,
			"/claude":   claude,
		},
	})
	if err != nil {
		t.Fatalf("ProbeAll() error = %v", err)
	}
	return reports
}

func detected(version string) fakeRun {
	return fakeRun{execution: runtimeprobe.Execution{Stdout: []byte(version + "\n")}}
}

func claudeDetected(version string) fakeRun {
	return fakeRun{execution: runtimeprobe.Execution{Stdout: []byte(version + " (Claude Code)")}}
}

func TestEvaluateUsesExactVersionsInCanonicalOrder(t *testing.T) {
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{
		{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3"}},
		{ID: runtimematrix.RuntimeOpenCode, KnownIncompatible: []string{"2.3.4"}},
		{ID: runtimematrix.RuntimeClaudeCode},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name    string
		reports []runtimeprobe.Report
		want    []runtimematrix.Observation
	}{
		{
			name:    "exact compatible and incompatible entries",
			reports: reports(t, detected("1.2.3"), detected("2.3.4"), claudeDetected("3.4.5")),
			want: []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: true, Version: "1.2.3", Compatibility: runtimematrix.Compatible},
				{ID: runtimematrix.RuntimeOpenCode, Present: true, Version: "2.3.4", Compatibility: runtimematrix.Incompatible},
				{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
			},
		},
		{
			name:    "near versions do not match",
			reports: reports(t, detected("1.2.4"), detected("2.3.5"), claudeDetected("3.4.6")),
			want: []runtimematrix.Observation{
				{ID: runtimematrix.RuntimePi, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeOpenCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
				{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Evaluate(tt.reports)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Evaluate() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNewPolicyRejectsInvalidOrAliasedEntries(t *testing.T) {
	valid := func() []runtimecompat.Entry {
		return []runtimecompat.Entry{
			{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3"}},
			{ID: runtimematrix.RuntimeOpenCode, KnownIncompatible: []string{"2.3.4"}},
			{ID: runtimematrix.RuntimeClaudeCode},
		}
	}
	for _, tt := range []struct {
		name    string
		entries []runtimecompat.Entry
	}{
		{"unknown runtime", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimeOpenCode}, {ID: "other"}}},
		{"missing runtime", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimeOpenCode}}},
		{"duplicate runtime", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimeClaudeCode}}},
		{"empty version", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{""}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}}},
		{"malformed version", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"v1.2.3"}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}}},
		{"duplicate version", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3", "1.2.3"}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}}},
		{"overlapping version", []runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3"}, KnownIncompatible: []string{"1.2.3"}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runtimecompat.NewPolicy(tt.entries); err == nil {
				t.Fatal("NewPolicy() error = nil")
			}
		})
	}

	entries := valid()
	policy, err := runtimecompat.NewPolicy(entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].CertifiedCompatible[0] = "9.9.9"
	entries[1].KnownIncompatible[0] = "9.9.9"
	got, err := policy.Evaluate(reports(t, detected("1.2.3"), detected("2.3.4"), claudeDetected("3.4.5")))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Compatibility != runtimematrix.Compatible || got[1].Compatibility != runtimematrix.Incompatible {
		t.Fatalf("policy retained mutable input aliases: %#v", got)
	}
}

func TestEvaluateKeepsAbsentAndProbeFailuresUnknown(t *testing.T) {
	got, err := runtimecompat.BuiltInPolicy().Evaluate(reports(t,
		fakeRun{},
		fakeRun{execution: runtimeprobe.Execution{ExitCode: 1}},
		fakeRun{execution: runtimeprobe.Execution{Stdout: []byte("not a version")}},
		"pi",
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []runtimematrix.Observation{
		{ID: runtimematrix.RuntimePi, Present: false, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeOpenCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
		{ID: runtimematrix.RuntimeClaudeCode, Present: true, Compatibility: runtimematrix.CompatibilityUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Evaluate() = %#v, want %#v", got, want)
	}
}

func TestBuiltInPolicyDoesNotCertifyObservedCandidates(t *testing.T) {
	got, err := runtimecompat.BuiltInPolicy().Evaluate(reports(t,
		detected("0.84.3"), detected("1.18.21"), claudeDetected("2.1.243"),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range got {
		if observation.Compatibility != runtimematrix.CompatibilityUnknown {
			t.Fatalf("BuiltInPolicy() certified %s: %#v", observation.ID, observation)
		}
	}
}
