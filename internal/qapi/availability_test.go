package qapi

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// readBackendFixture reads one committed probe capture of one backend.
func readBackendFixture(t *testing.T, backend, version string, parts ...string) []byte {
	t.Helper()
	data, err := os.ReadFile(backendTestdata(backend, version, parts...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func claudeAuthFixture(t *testing.T, name string) []byte {
	t.Helper()
	return readBackendFixture(t, claudeFixtureBackend, claudeFixtureVersion, "auth", name)
}

func openCodeFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	return readBackendFixture(t, opencodeFixtureBackend, opencodeFixtureVersion, parts...)
}

// TestProbeClaudeAuth reads the two committed `claude auth status` captures.
// The authenticated one exits 0 and reports loggedIn true; the unauthenticated
// one exits 1 and reports loggedIn false. Both are well-formed documents, so
// the unauthenticated case is a typed availability answer and never a parser
// rejection.
func TestProbeClaudeAuth(t *testing.T) {
	authenticated := claudeAuthFixture(t, "status-authenticated.json")
	unauthenticated := claudeAuthFixture(t, "status-unauthenticated.json")
	tests := []struct {
		name  string
		input BackendProbeInput
		ready bool
		code  qaadmission.Code
	}{
		{"observed authenticated capture is ready", BackendProbeInput{Stdout: authenticated, Complete: true}, true, ""},
		{"observed unauthenticated capture is not ready", BackendProbeInput{Stdout: unauthenticated, ExitCode: 1, Complete: true}, false, qaadmission.CodeAuthNotReady},
		{"logged in with a failing exit is contradictory", BackendProbeInput{Stdout: authenticated, ExitCode: 1, Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"missing loggedIn fails normalization", BackendProbeInput{Stdout: []byte(`{"authMethod":"none"}`), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"non-boolean loggedIn fails normalization", BackendProbeInput{Stdout: []byte(`{"loggedIn":"yes"}`), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"malformed document fails normalization", BackendProbeInput{Stdout: []byte("not json\n"), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"invalid UTF-8 fails normalization", BackendProbeInput{Stdout: []byte{0xff}, Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"overflow fails normalization", BackendProbeInput{Stdout: bytes.Repeat([]byte("x"), maxAuthOutputBytes+1), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"truncated capture fails normalization", BackendProbeInput{Stdout: authenticated}, false, qaadmission.CodeNormalizationFailed},
		{"silent output fails normalization", BackendProbeInput{Complete: true}, false, qaadmission.CodeNormalizationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbeClaudeAuth(test.input)
			if result.Ready != test.ready || result.Code != test.code {
				t.Fatalf("ProbeClaudeAuth() = %#v, want ready=%t code=%q", result, test.ready, test.code)
			}
		})
	}
}

// TestProbeOpenCodeModel reads the two committed `opencode models` captures.
// The authenticated capture lists the route's nan models; the unauthenticated
// one lists only the provider-free models, which is what makes this command an
// honest answer to "can this install reach the resolved route".
func TestProbeOpenCodeModel(t *testing.T) {
	available := openCodeFixture(t, "models", "available.txt")
	unavailable := openCodeFixture(t, "models", "unavailable.txt")
	tests := []struct {
		name            string
		input           BackendProbeInput
		provider, model string
		want            bool
		code            qaadmission.Code
	}{
		{"observed route model is available", BackendProbeInput{Stdout: available, Complete: true}, "nan", "qwen3.6", true, ""},
		{"unauthenticated capture drops the route provider", BackendProbeInput{Stdout: unavailable, Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeModelUnavailable},
		{"absent model is unavailable", BackendProbeInput{Stdout: available, Complete: true}, "nan", "qwen9.9-absent", false, qaadmission.CodeModelUnavailable},
		{"wrong provider does not match", BackendProbeInput{Stdout: available, Complete: true}, "openai", "qwen3.6", false, qaadmission.CodeModelUnavailable},
		{"duplicate row fails normalization", BackendProbeInput{Stdout: append(append([]byte{}, available...), []byte("nan/qwen3.6\n")...), Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"unqualified row fails normalization", BackendProbeInput{Stdout: []byte("qwen3.6\n"), Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"unterminated capture fails normalization", BackendProbeInput{Stdout: []byte("nan/qwen3.6"), Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"nonzero exit fails normalization", BackendProbeInput{Stdout: available, ExitCode: 1, Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"truncated capture fails normalization", BackendProbeInput{Stdout: available}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"invalid UTF-8 fails normalization", BackendProbeInput{Stdout: []byte{0xff}, Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
		{"silent output fails normalization", BackendProbeInput{Complete: true}, "nan", "qwen3.6", false, qaadmission.CodeNormalizationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbeOpenCodeModel(test.input, test.provider, test.model)
			if result.Available != test.want || result.Code != test.code {
				t.Fatalf("ProbeOpenCodeModel() = %#v, want available=%t code=%q", result, test.want, test.code)
			}
		})
	}
}

// TestProbeOpenCodeAuth reads the two committed `opencode auth list` captures.
// The command has no machine format, so the parser reads the one line it
// prints in both states — the credential count — and nothing else.
func TestProbeOpenCodeAuth(t *testing.T) {
	present := openCodeFixture(t, "auth", "credentials-present.txt")
	absent := openCodeFixture(t, "auth", "credentials-absent.txt")
	tests := []struct {
		name  string
		input BackendProbeInput
		ready bool
		code  qaadmission.Code
	}{
		{"observed credential capture is ready", BackendProbeInput{Stdout: present, Complete: true}, true, ""},
		{"observed empty credential capture is not ready", BackendProbeInput{Stdout: absent, Complete: true}, false, qaadmission.CodeAuthNotReady},
		{"no credential line fails normalization", BackendProbeInput{Stdout: []byte("Credentials\n"), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"two credential lines fail normalization", BackendProbeInput{Stdout: append(append([]byte{}, absent...), present...), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"nonzero exit fails normalization", BackendProbeInput{Stdout: present, ExitCode: 1, Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"truncated capture fails normalization", BackendProbeInput{Stdout: present}, false, qaadmission.CodeNormalizationFailed},
		{"invalid UTF-8 fails normalization", BackendProbeInput{Stdout: []byte{0xff}, Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"overflow fails normalization", BackendProbeInput{Stdout: bytes.Repeat([]byte("x"), maxAuthOutputBytes+1), Complete: true}, false, qaadmission.CodeNormalizationFailed},
		{"silent output fails normalization", BackendProbeInput{Complete: true}, false, qaadmission.CodeNormalizationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ProbeOpenCodeAuth(test.input)
			if result.Ready != test.ready || result.Code != test.code {
				t.Fatalf("ProbeOpenCodeAuth() = %#v, want ready=%t code=%q", result, test.ready, test.code)
			}
		})
	}
}

// probeRecorder replaces the shared bounded probe runner so a test can pin the
// exact commands a backend issues, in order, without launching anything.
type probeRecorder struct {
	issued   [][]string
	captures []probeCapture
}

func (recorder *probeRecorder) install(t *testing.T) {
	t.Helper()
	original := executeProbeCommand
	executeProbeCommand = func(_ context.Context, _, _ string, arguments []string, _, _ int) probeCapture {
		recorder.issued = append(recorder.issued, arguments)
		if len(recorder.captures) == 0 {
			return probeCapture{startFailed: true}
		}
		capture := recorder.captures[0]
		recorder.captures = recorder.captures[1:]
		return capture
	}
	t.Cleanup(func() { executeProbeCommand = original })
}

func completeCapture(stdout []byte, exitCode int) probeCapture {
	return probeCapture{started: true, stdout: stdout, exitCode: exitCode}
}

func availabilityRoute(backend string) qaroute.ResolvedRoute {
	return qaroute.ResolvedRoute{
		PolicyVersion: qaroute.PolicyVersion, Role: qarole.RequirementsAnalyst, Backend: backend,
		Provider: "nan", Model: "qwen3.6", Effort: "high", ProfileID: "role-default",
	}
}

func availabilityCWD(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

// TestPiAvailabilityProbeIsUnchanged pins the exact two Pi probe commands, in
// order, reached through the backend port. Decision B generalized the shape Pi
// already had; Pi's own probe contract must not move with it.
func TestPiAvailabilityProbeIsUnchanged(t *testing.T) {
	cwd := availabilityCWD(t)
	recorder := &probeRecorder{captures: []probeCapture{
		completeCapture([]byte(modelTableHeader+"\nnan           qwen3.6              1        1        yes       no\n"), 0),
		completeCapture([]byte(`{"status":"ready","provider":"nan","authType":"api_key"}`+"\n"), 0),
	}}
	recorder.install(t)
	bound := boundPi{path: filepath.Join(cwd, "pi"), cwd: cwd, version: RuntimeVersion, versionCapture: successfulVersion()}
	verdict := NewPiBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(piBackendID))
	if !verdict.Available || verdict.Code != "" {
		t.Fatalf("verdict = %#v", verdict)
	}
	want := [][]string{
		{"--list-models"},
		{"auth", "check", "--provider", "nan", "--json", "--no-refresh"},
	}
	if len(recorder.issued) != len(want) {
		t.Fatalf("issued %d probe commands, want %d: %v", len(recorder.issued), len(want), recorder.issued)
	}
	for index := range want {
		if len(recorder.issued[index]) != len(want[index]) {
			t.Fatalf("probe %d = %v, want %v", index, recorder.issued[index], want[index])
		}
		for position := range want[index] {
			if recorder.issued[index][position] != want[index][position] {
				t.Fatalf("probe %d = %v, want %v", index, recorder.issued[index], want[index])
			}
		}
	}
}

// TestClaudeAvailabilityProbe pins the one command the Claude Code backend
// issues before a run, and both verdicts it can reach from it.
func TestClaudeAvailabilityProbe(t *testing.T) {
	cwd := availabilityCWD(t)
	bound := boundClaude{executable: executableIdentity{path: filepath.Join(cwd, "claude")}, cwd: cwd, version: claudeFixtureVersion}
	t.Run("authenticated", func(t *testing.T) {
		recorder := &probeRecorder{captures: []probeCapture{completeCapture(claudeAuthFixture(t, "status-authenticated.json"), 0)}}
		recorder.install(t)
		verdict := NewClaudeBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(claudeBackendID))
		if !verdict.Available || verdict.Code != "" {
			t.Fatalf("verdict = %#v", verdict)
		}
		want := []string{"auth", "status", "--json"}
		if len(recorder.issued) != 1 || len(recorder.issued[0]) != len(want) {
			t.Fatalf("issued = %v, want one %v", recorder.issued, want)
		}
		for index := range want {
			if recorder.issued[0][index] != want[index] {
				t.Fatalf("issued = %v, want %v", recorder.issued[0], want)
			}
		}
	})
	t.Run("unauthenticated", func(t *testing.T) {
		recorder := &probeRecorder{captures: []probeCapture{completeCapture(claudeAuthFixture(t, "status-unauthenticated.json"), 1)}}
		recorder.install(t)
		verdict := NewClaudeBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(claudeBackendID))
		if verdict.Available || verdict.Code != qaadmission.CodeAuthNotReady {
			t.Fatalf("verdict = %#v", verdict)
		}
	})
}

// TestOpenCodeAvailabilityProbe pins the two commands the OpenCode backend
// issues before a run, in order, and the verdict each capture produces.
func TestOpenCodeAvailabilityProbe(t *testing.T) {
	cwd := availabilityCWD(t)
	bound := boundOpenCode{executable: executableIdentity{path: filepath.Join(cwd, "opencode")}, cwd: cwd, version: opencodeFixtureVersion}
	t.Run("available", func(t *testing.T) {
		recorder := &probeRecorder{captures: []probeCapture{
			completeCapture(openCodeFixture(t, "models", "available.txt"), 0),
			completeCapture(openCodeFixture(t, "auth", "credentials-present.txt"), 0),
		}}
		recorder.install(t)
		verdict := NewOpenCodeBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(opencodeBackendID))
		if !verdict.Available || verdict.Code != "" {
			t.Fatalf("verdict = %#v", verdict)
		}
		want := [][]string{{"models", "--pure"}, {"auth", "list", "--pure"}}
		if len(recorder.issued) != len(want) {
			t.Fatalf("issued = %v, want %v", recorder.issued, want)
		}
		for index := range want {
			if len(recorder.issued[index]) != len(want[index]) {
				t.Fatalf("probe %d = %v, want %v", index, recorder.issued[index], want[index])
			}
			for position := range want[index] {
				if recorder.issued[index][position] != want[index][position] {
					t.Fatalf("probe %d = %v, want %v", index, recorder.issued[index], want[index])
				}
			}
		}
	})
	t.Run("model unavailable stops before the auth probe", func(t *testing.T) {
		recorder := &probeRecorder{captures: []probeCapture{completeCapture(openCodeFixture(t, "models", "unavailable.txt"), 0)}}
		recorder.install(t)
		verdict := NewOpenCodeBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(opencodeBackendID))
		if verdict.Available || verdict.Code != qaadmission.CodeModelUnavailable {
			t.Fatalf("verdict = %#v", verdict)
		}
		if len(recorder.issued) != 1 {
			t.Fatalf("issued = %v, want only the model probe", recorder.issued)
		}
	})
	t.Run("no credentials is not ready", func(t *testing.T) {
		recorder := &probeRecorder{captures: []probeCapture{
			completeCapture(openCodeFixture(t, "models", "available.txt"), 0),
			completeCapture(openCodeFixture(t, "auth", "credentials-absent.txt"), 0),
		}}
		recorder.install(t)
		verdict := NewOpenCodeBackend(nil).ProbeAvailability(context.Background(), bound, availabilityRoute(opencodeBackendID))
		if verdict.Available || verdict.Code != qaadmission.CodeAuthNotReady {
			t.Fatalf("verdict = %#v", verdict)
		}
	})
}

// TestAvailabilityProbeRejectsAForeignBinding proves a backend refuses to
// probe a binding another backend produced, so an adapter can never run its
// fixed commands against another runtime's executable.
func TestAvailabilityProbeRejectsAForeignBinding(t *testing.T) {
	cwd := availabilityCWD(t)
	recorder := &probeRecorder{}
	recorder.install(t)
	for _, test := range []struct {
		name    string
		backend Backend
		bound   BoundRuntime
	}{
		{"pi rejects a claude binding", NewPiBackend(nil), boundClaude{executable: executableIdentity{path: filepath.Join(cwd, "claude")}, cwd: cwd}},
		{"claude rejects a pi binding", NewClaudeBackend(nil), boundPi{path: filepath.Join(cwd, "pi"), cwd: cwd}},
		{"opencode rejects a claude binding", NewOpenCodeBackend(nil), boundClaude{executable: executableIdentity{path: filepath.Join(cwd, "claude")}, cwd: cwd}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict := test.backend.ProbeAvailability(context.Background(), test.bound, availabilityRoute(test.backend.ID()))
			if verdict.Available || verdict.Code != qaadmission.CodeUnsupportedRuntime {
				t.Fatalf("verdict = %#v", verdict)
			}
		})
	}
	if len(recorder.issued) != 0 {
		t.Fatalf("a foreign binding launched %v", recorder.issued)
	}
}

// TestReportFailureCode pins the mapping T3 flagged: a backend that reported
// its own terminal failure is an execution failure, not a malformed stream,
// and the availability codes keep exactly one producer — the pre-run probe.
func TestReportFailureCode(t *testing.T) {
	for _, test := range []struct {
		reason string
		want   qaadmission.Code
	}{
		{reportReasonBackendUnavailable, qaadmission.CodeExecutionFailed},
		{reportReasonBackendFailed, qaadmission.CodeExecutionFailed},
		{reportReasonMalformedRecord, qaadmission.CodeNormalizationFailed},
		{reportReasonToolCall, qaadmission.CodeNormalizationFailed},
		{reportReasonBlank, qaadmission.CodeNormalizationFailed},
	} {
		t.Run(test.reason, func(t *testing.T) {
			got := reportFailureCode(&ReportNormalizationError{reportStageResult, test.reason})
			if got != test.want {
				t.Fatalf("reportFailureCode(%q) = %q, want %q", test.reason, got, test.want)
			}
		})
	}
}
