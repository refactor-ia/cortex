package qapi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// openCodeCapture loads one committed OpenCode stream capture. As in the
// Claude Code suite, every test here starts from a real captured stream rather
// than a hand-written shape, so a rejection proves the parser rejects
// something the CLI could actually emit and an acceptance proves it accepts
// what the CLI does emit.
func openCodeCapture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(backendTestdata(opencodeFixtureBackend, opencodeFixtureVersion, "responses", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return data
}

func TestOpenCodeBackendIdentity(t *testing.T) {
	if id := NewOpenCodeBackend(nil).ID(); id != "opencode" {
		t.Fatalf("backend identity = %q, want %q", id, "opencode")
	}
}

// TestOpenCodeArgvContract pins the exact token contract. --pure is the
// isolation token and is asserted verbatim: without it the invoking operator's
// external plugins load and inject context into the run.
func TestOpenCodeArgvContract(t *testing.T) {
	want := []string{"run", "--format", "json", "--pure"}
	if got := openCodeArgv(); !slices.Equal(got, want) {
		t.Fatalf("opencode argv = %q, want %q", got, want)
	}
}

// TestOpenCodeArgvOptsOutOfSessionContinuation proves no token resumes,
// forks, or shares a session: every run is a fresh, unshared session, which is
// the no-session property the QA vertical depends on.
func TestOpenCodeArgvOptsOutOfSessionContinuation(t *testing.T) {
	for _, forbidden := range []string{"-c", "--continue", "-s", "--session", "--fork", "--share", "--auto", "--attach"} {
		if slices.Contains(openCodeArgv(), forbidden) {
			t.Fatalf("argv carries %q", forbidden)
		}
	}
}

// TestOpenCodeInvocationCarriesNoRoleInput proves the role input never reaches
// argv. `opencode run` takes the message as a positional argument but also
// reads it from stdin, and stdin is what this adapter uses.
func TestOpenCodeInvocationCarriesNoRoleInput(t *testing.T) {
	for _, argument := range openCodeArgv() {
		if bytes.Contains([]byte(argument), []byte("cortex-")) {
			t.Fatalf("argv carries role input: %q", argument)
		}
	}
}

func TestParseOpenCodeReportStreamSuccess(t *testing.T) {
	report, diagnostic := parseOpenCodeReportStream(openCodeCapture(t, "stream-success.json"))
	if diagnostic != nil {
		t.Fatalf("captured success stream rejected: %v", diagnostic)
	}
	if report != "OK" {
		t.Fatalf("report = %q, want %q", report, "OK")
	}
}

// TestParseOpenCodeReportStreamSkipsUnknownEvents covers the same deliberate
// difference from the Pi parser the Claude adapter records: the emitter also
// produces reasoning, file, patch and agent events that carry no report
// content, so an unknown event must be skipped rather than rejected.
func TestParseOpenCodeReportStreamSkipsUnknownEvents(t *testing.T) {
	lines := streamLines(t, openCodeCapture(t, "stream-success.json"))
	index := streamEventIndex(t, lines, "step_finish")
	injected := slices.Insert(slices.Clone(lines), index, []byte(`{"type":"reasoning","part":{"type":"reasoning","text":"fixture"}}`))
	report, diagnostic := parseOpenCodeReportStream(joinStream(injected))
	if diagnostic != nil {
		t.Fatalf("unknown event rejected: %v", diagnostic)
	}
	if report != "OK" {
		t.Fatalf("report = %q, want %q", report, "OK")
	}
}

// TestParseOpenCodeReportStreamFailureEvent pins the typed failure shape.
// OpenCode reports a provider, credential or model failure as one `error`
// event with a name and an opaque ref, and exits non-zero. It must be named as
// a backend failure, never as a generic parse rejection and never as a silent
// fallback to another backend.
func TestParseOpenCodeReportStreamFailureEvent(t *testing.T) {
	report, diagnostic := parseOpenCodeReportStream(openCodeCapture(t, "stream-error.json"))
	if report != "" {
		t.Fatalf("report = %q, want empty", report)
	}
	if diagnostic == nil {
		t.Fatalf("error stream accepted")
	}
	if diagnostic.Stage != reportStageResult || diagnostic.Reason != reportReasonBackendFailed {
		t.Fatalf("diagnostic = %s/%s, want %s/%s", diagnostic.Stage, diagnostic.Reason, reportStageResult, reportReasonBackendFailed)
	}
}

// TestParseOpenCodeReportStreamRejections drives every fixed rejection
// condition from a mutation of the real captured success stream and pins the
// named reason. A diagnostic never carries stream content, which the loop
// asserts.
func TestParseOpenCodeReportStreamRejections(t *testing.T) {
	capture := openCodeCapture(t, "stream-success.json")
	lines := streamLines(t, capture)
	start := streamEventIndex(t, lines, "step_start")
	text := streamEventIndex(t, lines, "text")
	finish := streamEventIndex(t, lines, "step_finish")

	cases := []struct {
		name   string
		stream []byte
		stage  string
		reason string
	}{
		{"empty stream", nil, reportStageStream, reportReasonEmpty},
		{"unterminated stream", capture[:len(capture)-1], reportStageStream, reportReasonUnterminated},
		{"invalid utf8", append(slices.Clone(capture), 0xff, '\n'), reportStageStream, reportReasonInvalidUTF8},
		{"malformed record", joinStream(append(slices.Clone(lines), []byte(`{"type":`))), reportStageStream, reportReasonMalformedRecord},
		{"missing type", joinStream(append(slices.Clone(lines), []byte(`{"part":{}}`))), reportStageEnvelope, reportReasonMissingType},
		{"missing step_finish", joinStream(slices.Delete(slices.Clone(lines), finish, finish+1)), reportStageStream, reportReasonIncomplete},
		{"missing step_start", joinStream(slices.Delete(slices.Clone(lines), start, start+1)), reportStageEnvelope, reportReasonUnexpected},
		{"missing text", joinStream(slices.Delete(slices.Clone(lines), text, text+1)), reportStageMessage, reportReasonIncomplete},
		{
			name:   "tool use in the stream",
			stream: joinStream(slices.Insert(slices.Clone(lines), text, []byte(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed"}}}`))),
			stage:  reportStageMessage, reason: reportReasonToolCall,
		},
		{
			name:   "more than one turn",
			stream: joinStream(slices.Insert(slices.Clone(lines), finish, lines[start])),
			stage:  reportStageResult, reason: reportReasonTurnMismatch,
		},
		{
			name:   "step_finish after the terminal step_finish",
			stream: joinStream(append(slices.Clone(lines), lines[finish])),
			stage:  reportStageResult, reason: reportReasonTurnMismatch,
		},
		{
			name: "step_finish that did not stop",
			stream: joinStream(mutateStreamEvent(t, lines, finish, func(event map[string]any) {
				part, _ := event["part"].(map[string]any)
				part["reason"] = "tool-calls"
			})),
			stage: reportStageResult, reason: reportReasonTurnMismatch,
		},
		{
			name: "missing stop reason",
			stream: joinStream(mutateStreamEvent(t, lines, finish, func(event map[string]any) {
				part, _ := event["part"].(map[string]any)
				delete(part, "reason")
			})),
			stage: reportStageResult, reason: reportReasonTurnMismatch,
		},
		{
			name:   "more than one text event",
			stream: joinStream(slices.Insert(slices.Clone(lines), finish, lines[text])),
			stage:  reportStageMessage, reason: reportReasonExtraMessages,
		},
		{
			name: "non-string terminal text",
			stream: joinStream(mutateStreamEvent(t, lines, text, func(event map[string]any) {
				part, _ := event["part"].(map[string]any)
				part["text"] = []any{"OK"}
			})),
			stage: reportStageMessage, reason: reportReasonInvalidText,
		},
		{
			name: "text event without a part",
			stream: joinStream(mutateStreamEvent(t, lines, text, func(event map[string]any) {
				event["part"] = "OK"
			})),
			stage: reportStageMessage, reason: reportReasonInvalidMessage,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			report, diagnostic := parseOpenCodeReportStream(testCase.stream)
			if report != "" {
				t.Fatalf("report = %q, want empty", report)
			}
			if diagnostic == nil {
				t.Fatalf("stream accepted")
			}
			if diagnostic.Stage != testCase.stage || diagnostic.Reason != testCase.reason {
				t.Fatalf("diagnostic = %s/%s, want %s/%s", diagnostic.Stage, diagnostic.Reason, testCase.stage, testCase.reason)
			}
			if bytes.Contains([]byte(diagnostic.Error()), []byte("OK")) {
				t.Fatalf("diagnostic echoes stream content: %q", diagnostic.Error())
			}
		})
	}
}

// TestOpenCodeBackendParseReportSuccess covers the port method rather than the
// parser function, so the Backend contract — nil diagnostic is the only
// success signal — is pinned too.
func TestOpenCodeBackendParseReportSuccess(t *testing.T) {
	report, diagnostic := NewOpenCodeBackend(nil).ParseReport(openCodeCapture(t, "stream-success.json"))
	if diagnostic != nil || report != "OK" {
		t.Fatalf("ParseReport = %q, %v; want %q, nil", report, diagnostic, "OK")
	}
}

// TestOpenCodeRouteIsNotAdmittedYet records the route-policy boundary, exactly
// as the Claude adapter does. The adapter re-resolves the route against its own
// backend identity instead of trusting the value it was handed, so until route
// policy admits "opencode" no invocation and no input frame can be built for
// it. Admitting the backend is a deliberate policy change owned by the
// receipt-contract slice, not a side effect of this adapter landing.
func TestOpenCodeRouteIsNotAdmittedYet(t *testing.T) {
	route := qaroute.ResolvedRoute{
		PolicyVersion: qaroute.PolicyVersion, Role: qarole.RequirementsAnalyst, Backend: "opencode",
		Provider: "nan", Model: "qwen3.6", Effort: "medium", ProfileID: "role-default",
	}
	backend := NewOpenCodeBackend(nil)
	if _, err := backend.BuildInvocation(route, BoundInvocationPaths{}); err == nil {
		t.Fatalf("BuildInvocation accepted a route policy does not admit")
	}
	if _, err := backend.EncodeInput(route, "", "", []byte("task")); err == nil {
		t.Fatalf("EncodeInput accepted a route policy does not admit")
	}
}

// openCodeVersion is the fixed `opencode --version` line of the captured build.
func openCodeVersion() versionCapture {
	return versionCapture{started: true, stdout: []byte(opencodeFixtureVersion + "\n")}
}

// TestOpenCodeBinding proves the binding verifies the executable and records
// the observed version, and that the version probe runs once against the
// canonical path rather than the resolver's candidate.
func TestOpenCodeBinding(t *testing.T) {
	original := executeVersionCommand
	probed := ""
	executeVersionCommand = func(_ context.Context, path, _ string) versionCapture {
		probed = path
		return openCodeVersion()
	}
	t.Cleanup(func() { executeVersionCommand = original })

	cwd := runtimeDirectory(t)
	resolver := &runtimeResolver{path: runtimeBinary(t, "opencode")}
	bound, err := bindOpenCode(context.Background(), cwd, resolver)
	if err != nil {
		t.Fatalf("bindOpenCode() = %v", err)
	}
	if probed != resolver.path {
		t.Fatalf("version probed %q, want %q", probed, resolver.path)
	}
	if bound.Path() != resolver.path || bound.CWD() != cwd {
		t.Fatalf("bound = %q %q, want %q %q", bound.Path(), bound.CWD(), resolver.path, cwd)
	}
	if bound.version != opencodeFixtureVersion || bound.executable.size <= 0 {
		t.Fatalf("bound version = %q, size = %d", bound.version, bound.executable.size)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
}

// TestOpenCodeBindingRejectsVersionCaptures pins the version contract,
// including the fact that it is OpenCode's own: `opencode --version` prints a
// bare semantic version, so neither Pi's nor Claude Code's decorated line binds
// anything here.
func TestOpenCodeBindingRejectsVersionCaptures(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		capture versionCapture
	}{
		{"pi version line", versionCapture{started: true, stdout: []byte("pi 0.85.1\n")}},
		{"claude version line", versionCapture{started: true, stdout: []byte("2.1.278 (Claude Code)\n")}},
		{"malformed version", versionCapture{started: true, stdout: []byte("1.18\n")}},
		{"silent version", versionCapture{started: true}},
		{"nonzero version", versionCapture{started: true, stdout: []byte(opencodeFixtureVersion + "\n"), exitCode: 2}},
		{"incomplete capture", versionCapture{started: true, stdout: []byte(opencodeFixtureVersion + "\n"), stdoutTruncated: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			original := executeVersionCommand
			executeVersionCommand = func(context.Context, string, string) versionCapture { return testCase.capture }
			t.Cleanup(func() { executeVersionCommand = original })
			if _, err := bindOpenCode(context.Background(), runtimeDirectory(t), &runtimeResolver{path: runtimeBinary(t, "opencode")}); err == nil {
				t.Fatal("bindOpenCode() succeeded")
			}
		})
	}
}

// TestOpenCodeBindingRejectsUnresolvableRuntime covers the not-installed case:
// it is an error at bind time, never a fallback to another backend.
func TestOpenCodeBindingRejectsUnresolvableRuntime(t *testing.T) {
	if _, err := bindOpenCode(context.Background(), runtimeDirectory(t), failingOpenCodeResolver{}); err == nil {
		t.Fatal("bindOpenCode() succeeded without a runtime")
	}
	if _, err := bindOpenCode(context.Background(), runtimeDirectory(t), nil); err == nil {
		t.Fatal("bindOpenCode() succeeded without a resolver")
	}
}

type failingOpenCodeResolver struct{}

func (failingOpenCodeResolver) Resolve(context.Context) (string, error) {
	return "", errors.New("opencode is not installed")
}
