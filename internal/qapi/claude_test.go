package qapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// claudeCapture loads one committed Claude Code stream capture. Every test in
// this file starts from a real captured stream rather than a hand-written
// shape, so a rejection proves the parser rejects something the CLI could
// actually emit and an acceptance proves it accepts what the CLI does emit.
func claudeCapture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(backendTestdata(claudeFixtureBackend, claudeFixtureVersion, "responses", name))
	if err != nil {
		t.Fatalf("read capture %s: %v", name, err)
	}
	return data
}

// claudeStreamLines splits one captured stream into its events; the captures
// are JSON Lines and always end with a terminating newline.
func claudeStreamLines(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	if len(stream) == 0 || stream[len(stream)-1] != '\n' {
		t.Fatalf("capture is not newline terminated")
	}
	return bytes.Split(stream[:len(stream)-1], []byte("\n"))
}

// claudeStream reassembles a JSON Lines stream from events.
func claudeStream(lines [][]byte) []byte {
	var stream bytes.Buffer
	for _, line := range lines {
		stream.Write(line)
		stream.WriteByte('\n')
	}
	return stream.Bytes()
}

// claudeEventIndex finds the first event of one type in a split capture.
func claudeEventIndex(t *testing.T, lines [][]byte, kind string) int {
	t.Helper()
	for index, line := range lines {
		var event map[string]any
		if json.Unmarshal(line, &event) == nil && event["type"] == kind {
			return index
		}
	}
	t.Fatalf("capture carries no %q event", kind)
	return -1
}

// claudeMutate rewrites one event of a split capture through a decoded map, so
// a mutation targets a field rather than a byte sequence of the capture.
func claudeMutate(t *testing.T, lines [][]byte, index int, mutate func(map[string]any)) [][]byte {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(lines[index], &event); err != nil {
		t.Fatalf("decode event %d: %v", index, err)
	}
	mutate(event)
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode event %d: %v", index, err)
	}
	mutated := slices.Clone(lines)
	mutated[index] = encoded
	return mutated
}

func TestClaudeBackendIdentity(t *testing.T) {
	if id := NewClaudeBackend(nil).ID(); id != "claude" {
		t.Fatalf("backend identity = %q, want %q", id, "claude")
	}
}

// TestClaudeArgvContract pins the exact token contract. The neutralization
// flags are the reason a headless run is evidence at all, so they are asserted
// verbatim: a capture taken without them carried the invoking operator's hooks
// and MCP servers into the stream.
func TestClaudeArgvContract(t *testing.T) {
	want := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "1",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--settings", `{"hooks":{},"disableAllHooks":true}`,
	}
	if got := claudeArgv(); !slices.Equal(got, want) {
		t.Fatalf("claude argv = %q, want %q", got, want)
	}
}

// TestClaudeInvocationCarriesNoRoleInput proves the role input never reaches
// argv: the prompt is framed on stdin, which keeps the bounded-input property,
// stays clear of argv size limits, and keeps role content out of the process
// table.
func TestClaudeInvocationCarriesNoRoleInput(t *testing.T) {
	for _, argument := range claudeArgv() {
		if bytes.Contains([]byte(argument), []byte("cortex-")) {
			t.Fatalf("argv carries role input: %q", argument)
		}
	}
}

func TestParseClaudeReportStreamSuccess(t *testing.T) {
	report, diagnostic := parseClaudeReportStream(claudeCapture(t, "stream-success.jsonl"))
	if diagnostic != nil {
		t.Fatalf("captured success stream rejected: %v", diagnostic)
	}
	if report != "OK" {
		t.Fatalf("report = %q, want %q", report, "OK")
	}
}

// TestParseClaudeReportStreamSkipsUnknownEvents covers the deliberate
// difference from the Pi parser: the captured success stream already carries a
// rate_limit_event between the assistant event and the result, so the sequence
// is not fixed and an unknown event must be skipped rather than rejected.
func TestParseClaudeReportStreamSkipsUnknownEvents(t *testing.T) {
	lines := claudeStreamLines(t, claudeCapture(t, "stream-success.jsonl"))
	index := claudeEventIndex(t, lines, "result")
	injected := slices.Insert(slices.Clone(lines), index, []byte(`{"type":"fixture_unknown_event","payload":{"nested":[1,2,3]}}`))
	report, diagnostic := parseClaudeReportStream(claudeStream(injected))
	if diagnostic != nil {
		t.Fatalf("unknown event rejected: %v", diagnostic)
	}
	if report != "OK" {
		t.Fatalf("report = %q, want %q", report, "OK")
	}
}

// TestParseClaudeReportStreamUnauthenticated pins the typed availability
// failure. An unauthenticated Claude Code emits a well-formed terminal result
// with is_error true and terminal_reason "api_error"; it must be named as a
// backend availability failure, never as a generic parse rejection and never
// as a silent fallback to another backend.
func TestParseClaudeReportStreamUnauthenticated(t *testing.T) {
	report, diagnostic := parseClaudeReportStream(claudeCapture(t, "stream-unauthenticated.jsonl"))
	if report != "" {
		t.Fatalf("report = %q, want empty", report)
	}
	if diagnostic == nil {
		t.Fatalf("unauthenticated stream accepted")
	}
	if diagnostic.Stage != reportStageResult || diagnostic.Reason != reportReasonBackendUnavailable {
		t.Fatalf("diagnostic = %s/%s, want %s/%s", diagnostic.Stage, diagnostic.Reason, reportStageResult, reportReasonBackendUnavailable)
	}
}

// TestParseClaudeReportStreamRejections drives every fixed rejection condition
// from a mutation of the real captured success stream and pins the named
// reason. A diagnostic never carries stream content, which the loop asserts.
func TestParseClaudeReportStreamRejections(t *testing.T) {
	capture := claudeCapture(t, "stream-success.jsonl")
	lines := claudeStreamLines(t, capture)
	assistant := claudeEventIndex(t, lines, "assistant")
	result := claudeEventIndex(t, lines, "result")
	system := claudeEventIndex(t, lines, "system")

	cases := []struct {
		name   string
		stream []byte
		stage  string
		reason string
	}{
		{"empty stream", nil, reportStageStream, reportReasonEmpty},
		{"unterminated stream", capture[:len(capture)-1], reportStageStream, reportReasonUnterminated},
		{"truncated stream", append(slices.Clone(capture[:len(capture)/2]), '\n'), reportStageStream, reportReasonMalformedRecord},
		{"invalid utf8", append(slices.Clone(capture), 0xff, '\n'), reportStageStream, reportReasonInvalidUTF8},
		{"malformed record", claudeStream(append(slices.Clone(lines), []byte(`{"type":`))), reportStageStream, reportReasonMalformedRecord},
		{"missing type", claudeStream(append(slices.Clone(lines), []byte(`{"subtype":"init"}`))), reportStageEnvelope, reportReasonMissingType},
		{"missing terminal result", claudeStream(slices.Delete(slices.Clone(lines), result, result+1)), reportStageStream, reportReasonIncomplete},
		{"missing session init", claudeStream(slices.Delete(slices.Clone(lines), system, system+1)), reportStageEnvelope, reportReasonUnexpected},
		{
			name: "tool use in the stream",
			stream: claudeStream(claudeMutate(t, lines, assistant, func(event map[string]any) {
				message, _ := event["message"].(map[string]any)
				message["content"] = []any{map[string]any{"type": "tool_use", "id": "toolu_fixture", "name": "Bash", "input": map[string]any{}}}
			})),
			stage: reportStageMessage, reason: reportReasonToolCall,
		},
		{
			name: "more than one turn",
			stream: claudeStream(claudeMutate(t, lines, result, func(event map[string]any) {
				event["num_turns"] = 2
			})),
			stage: reportStageResult, reason: reportReasonTurnMismatch,
		},
		{
			name:   "more than one assistant message",
			stream: claudeStream(slices.Insert(slices.Clone(lines), result, lines[assistant])),
			stage:  reportStageMessage, reason: reportReasonExtraMessages,
		},
		{
			name: "non-text assistant content",
			stream: claudeStream(claudeMutate(t, lines, assistant, func(event map[string]any) {
				message, _ := event["message"].(map[string]any)
				message["content"] = []any{map[string]any{"type": "image", "source": map[string]any{}}}
			})),
			stage: reportStageMessage, reason: reportReasonInvalidText,
		},
		{
			name: "non-string terminal text",
			stream: claudeStream(claudeMutate(t, lines, result, func(event map[string]any) {
				event["result"] = []any{"OK"}
			})),
			stage: reportStageResult, reason: reportReasonInvalidText,
		},
		{
			name:   "result after the terminal result",
			stream: claudeStream(append(slices.Clone(lines), lines[result])),
			stage:  reportStageResult, reason: reportReasonExtraMessages,
		},
		{
			name: "failed terminal result",
			stream: claudeStream(claudeMutate(t, lines, result, func(event map[string]any) {
				event["is_error"] = true
				event["terminal_reason"] = "refusal"
			})),
			stage: reportStageResult, reason: reportReasonBackendFailed,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			report, diagnostic := parseClaudeReportStream(testCase.stream)
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

// TestClaudeBackendParseReportSuccess covers the port method rather than the
// parser function, so the Backend contract — nil diagnostic is the only
// success signal — is pinned too.
func TestClaudeBackendParseReportSuccess(t *testing.T) {
	report, diagnostic := NewClaudeBackend(nil).ParseReport(claudeCapture(t, "stream-success.jsonl"))
	if diagnostic != nil || report != "OK" {
		t.Fatalf("ParseReport = %q, %v; want %q, nil", report, diagnostic, "OK")
	}
}

// TestClaudeRouteIsNotAdmittedYet records the route-policy boundary. The
// adapter re-resolves the route against its own backend identity instead of
// trusting the value it was handed, exactly as the Pi adapter does, so until
// route policy admits "claude" no invocation and no input frame can be built
// for it. Admitting the backend is a deliberate policy change owned by the
// receipt-contract slice, not a side effect of this adapter landing.
func TestClaudeRouteIsNotAdmittedYet(t *testing.T) {
	route := qaroute.ResolvedRoute{
		PolicyVersion: qaroute.PolicyVersion, Role: qarole.RequirementsAnalyst, Backend: "claude",
		Provider: "nan", Model: "qwen3.6", Effort: "medium", ProfileID: "role-default",
	}
	backend := NewClaudeBackend(nil)
	if _, err := backend.BuildInvocation(route, BoundInvocationPaths{}); err == nil {
		t.Fatalf("BuildInvocation accepted a route policy does not admit")
	}
	if _, err := backend.EncodeInput(route, "", "", []byte("task")); err == nil {
		t.Fatalf("EncodeInput accepted a route policy does not admit")
	}
}

// claudeVersion is the fixed `claude --version` line of the captured build.
func claudeVersion() versionCapture {
	return versionCapture{started: true, stdout: []byte(claudeFixtureVersion + " (Claude Code)\n")}
}

// TestClaudeBinding proves the binding verifies the executable and records the
// observed version, and that the version probe runs once against the canonical
// path rather than the resolver's candidate.
func TestClaudeBinding(t *testing.T) {
	original := executeVersionCommand
	probed := ""
	executeVersionCommand = func(_ context.Context, path, _ string) versionCapture {
		probed = path
		return claudeVersion()
	}
	t.Cleanup(func() { executeVersionCommand = original })

	cwd := runtimeDirectory(t)
	resolver := &runtimeResolver{path: runtimeBinary(t, "claude")}
	bound, err := bindClaude(context.Background(), cwd, resolver)
	if err != nil {
		t.Fatalf("bindClaude() = %v", err)
	}
	if probed != resolver.path {
		t.Fatalf("version probed %q, want %q", probed, resolver.path)
	}
	if bound.Path() != resolver.path || bound.CWD() != cwd {
		t.Fatalf("bound = %q %q, want %q %q", bound.Path(), bound.CWD(), resolver.path, cwd)
	}
	if bound.version != claudeFixtureVersion || bound.executable.size <= 0 {
		t.Fatalf("bound version = %q, size = %d", bound.version, bound.executable.size)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
}

// TestClaudeBindingRejectsVersionCaptures pins the version contract, including
// the fact that it is Claude Code's own and not Pi's: a pi version line binds
// nothing here.
func TestClaudeBindingRejectsVersionCaptures(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		capture versionCapture
	}{
		{"pi version line", versionCapture{started: true, stdout: []byte("pi 0.85.1\n")}},
		{"bare version", versionCapture{started: true, stdout: []byte(claudeFixtureVersion + "\n")}},
		{"malformed version", versionCapture{started: true, stdout: []byte("version 2.1 (Claude Code)\n")}},
		{"silent version", versionCapture{started: true}},
		{"nonzero version", versionCapture{started: true, stdout: []byte(claudeFixtureVersion + " (Claude Code)\n"), exitCode: 2}},
		{"incomplete capture", versionCapture{started: true, stdout: []byte(claudeFixtureVersion + " (Claude Code)\n"), stdoutTruncated: true}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			original := executeVersionCommand
			executeVersionCommand = func(context.Context, string, string) versionCapture { return testCase.capture }
			t.Cleanup(func() { executeVersionCommand = original })
			if _, err := bindClaude(context.Background(), runtimeDirectory(t), &runtimeResolver{path: runtimeBinary(t, "claude")}); err == nil {
				t.Fatal("bindClaude() succeeded")
			}
		})
	}
}

// TestClaudeBindingRejectsUnresolvableRuntime covers the not-installed case:
// it is an error at bind time, never a fallback to another backend.
func TestClaudeBindingRejectsUnresolvableRuntime(t *testing.T) {
	if _, err := bindClaude(context.Background(), runtimeDirectory(t), failingResolver{}); err == nil {
		t.Fatal("bindClaude() succeeded without a runtime")
	}
	if _, err := bindClaude(context.Background(), runtimeDirectory(t), nil); err == nil {
		t.Fatal("bindClaude() succeeded without a resolver")
	}
}

type failingResolver struct{}

func (failingResolver) Resolve(context.Context) (string, error) {
	return "", errors.New("claude is not installed")
}
