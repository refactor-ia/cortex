package qapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// claudeBackendID is the policy token for the Claude Code backend. It must
// match the backend the route policy admits; qaroute owns that set, and until
// it admits this token no route resolves and nothing here can be reached.
const claudeBackendID = "claude"

// The two neutralization payloads that make a headless Claude Code run
// isolated. They are not cosmetic and must not be "simplified" away.
//
// A headless run is NOT isolated by default. A capture taken without these
// flags carried the invoking operator's session hooks and MCP servers into the
// stream: they were loaded, they executed, and their output appeared in the
// events the parser reads. That is ambient configuration deciding what the
// report says, which is exactly the property this vertical exists to deny. An
// empty MCP server set with --strict-mcp-config replaces — rather than merges
// with — every discovered .mcp.json and user-level server, and an explicit
// settings object with no hooks and disableAllHooks replaces the operator's
// settings chain.
const (
	claudeEmptyMCPConfig  = `{"mcpServers":{}}`
	claudeNeutralSettings = `{"hooks":{},"disableAllHooks":true}`
)

// claudeVersionOutput matches the fixed `claude --version` line, for example
// "2.1.278 (Claude Code)".
var claudeVersionOutput = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+) \(Claude Code\)$`)

// claudeBackend runs one QA role on Claude Code's headless mode. It is the
// second Backend implementation and deliberately shares none of Pi's stream
// assumptions: only the port, the process bounds, and the input frame are
// common.
//
// The accepted limitation recorded with this feature applies here and is not
// worked around: `claude -p` runs Claude, so a role invoked through this
// backend is Claude reviewing Claude's output. The adapter restores isolation,
// bounded input, and a verifiable receipt; it does not restore model
// independence, and the receipt records backend identity so a consumer can
// tell a cross-model verdict from a same-model one.
type claudeBackend struct {
	resolver PathResolver
}

// NewClaudeBackend registers Claude Code as a QA backend. A nil resolver
// selects the production constrained PATH lookup, which is the only ambient
// state this package consults and only when the caller supplied no candidate.
func NewClaudeBackend(resolver PathResolver) Backend {
	if resolver == nil {
		resolver = lookPathClaude{}
	}
	return claudeBackend{resolver: resolver}
}

type lookPathClaude struct{}

func (lookPathClaude) Resolve(ctx context.Context) (string, error) {
	return exec.LookPath("claude")
}

func (claudeBackend) ID() string {
	return claudeBackendID
}

func (backend claudeBackend) Bind(ctx context.Context, cwd string) (BoundRuntime, error) {
	bound, err := bindClaude(ctx, cwd, backend.resolver)
	if err != nil {
		return nil, err
	}
	return bound, nil
}

// BuildInvocation constructs no process, shell, filesystem, or provider call.
// It re-resolves the route against this backend's own identity rather than
// trusting the value it was handed, so a route that was mutated after
// resolution — or resolved for another backend — cannot reach this argv.
//
// Unlike Pi, the actor and skill paths do not appear in argv: Claude Code has
// no --skill flag and its --append-system-prompt takes prompt text rather than
// a file path. The role, the actor digest, and the skill digest reach the
// process through the bounded stdin frame instead.
//
// The binding therefore carries no actor path at all: install ships this
// runtime no actor file, because nothing here would read one. The installed
// skill is still observed and digest-verified before anything is launched,
// and the actor is attested from catalog provenance — the same bytes the
// frame carries.
func (claudeBackend) BuildInvocation(route qaroute.ResolvedRoute, binding BoundInvocationPaths) (Invocation, error) {
	if !validRouteFor(route, claudeBackendID) || route.Role != binding.role || !absolutePaths(binding.binary, binding.skill, binding.cwd) {
		return Invocation{}, errors.New("invalid Claude Code invocation binding")
	}
	return Invocation{binary: binding.binary, cwd: binding.cwd, argv: claudeArgv()}, nil
}

// claudeArgv is the exact, input-independent token contract for one headless
// Claude Code run.
//
// The role input is not one of these tokens. `claude -p` accepts the prompt as
// a positional argument but also reads it from stdin, and stdin is what this
// adapter uses: it keeps the bounded-input property the shared runner already
// enforces, it cannot hit an argv size limit on a large task, and it keeps role
// content out of the process table. Verified against claude 2.1.278: piping
// the prompt with no positional argument produced the same
// system/init -> assistant -> rate_limit_event -> result stream.
//
// --max-turns 1 is the single-turn bound, and --verbose is required for
// stream-json to emit anything but the terminal event under -p.
func claudeArgv() []string {
	return []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--max-turns", "1",
		"--strict-mcp-config",
		"--mcp-config", claudeEmptyMCPConfig,
		"--settings", claudeNeutralSettings,
	}
}

// EncodeInput frames one bounded report input for Claude Code. The frame is
// the shared one: the backend token inside it differs, nothing else does, so a
// role receives the same instruction and the same identity block whichever
// runtime executes it.
func (claudeBackend) EncodeInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, task []byte) ([]byte, error) {
	return encodeReportInput(route, claudeBackendID, actorSHA256, skillSHA256, task)
}

func (claudeBackend) ParseReport(stdout []byte) (string, *ReportNormalizationError) {
	return parseClaudeReportStream(stdout)
}

// boundClaude is one verified Claude Code binding. It carries the same file
// identity the Pi binding does — so the executable cannot change between the
// version probe and the run — plus the observed version.
type boundClaude struct {
	executable     executableIdentity
	cwd, version   string
	versionCapture versionCapture
}

func (bound boundClaude) Path() string {
	return bound.executable.path
}

func (bound boundClaude) CWD() string {
	return bound.cwd
}

// bindClaude resolves and verifies Claude Code once for one run.
//
// It differs from bindPi in one deliberate way: it requires a well-formed
// version line and records it, but does not pin one exact version. Pi's stream
// parser is a strict state machine validated against one pinned build, so a
// different build is a contract risk there. Claude Code is the maintainer's own
// already-installed CLI, its stream is validated structurally at parse time,
// and an exact pin would reject working installations for no evidence gained.
// A build whose stream does not satisfy the parser still fails closed with a
// named reason rather than producing a report.
func bindClaude(ctx context.Context, cwd string, resolver PathResolver) (boundClaude, error) {
	if ctx == nil || resolver == nil {
		return boundClaude{}, errors.New("Claude Code binding dependencies are unavailable")
	}
	path, err := resolver.Resolve(ctx)
	if err != nil {
		return boundClaude{}, errors.New("Claude Code resolution failed")
	}
	canonicalCWD, err := canonicalRuntimeDirectory(cwd)
	if err != nil {
		return boundClaude{}, err
	}
	executable, err := inspectExecutable(path)
	if err != nil {
		return boundClaude{}, err
	}
	capture := executeVersionCommand(ctx, executable.path, canonicalCWD)
	version, valid := parseVersionWith(capture, claudeVersionOutput)
	if !valid {
		return boundClaude{}, errors.New("Claude Code version is unsupported")
	}
	after, err := inspectExecutable(executable.path)
	if err != nil || !sameExecutable(executable, after) {
		return boundClaude{}, errors.New("Claude Code changed during version binding")
	}
	return boundClaude{executable: executable, cwd: canonicalCWD, version: version, versionCapture: capture}, nil
}

// parseClaudeReportStream extracts one substantive report from a Claude Code
// stream-json event stream. On rejection it reports which fixed parser
// condition failed and never echoes stream content.
//
// It is intentionally not a strict state machine like the Pi parser. Claude
// Code interleaves transport events the report does not depend on — the
// captured success stream carries a rate_limit_event between the assistant
// message and the terminal result — so assuming a fixed sequence would reject
// a healthy run. Instead the parser fixes what it requires and ignores the
// rest:
//
//   - required, in this order: a leading system/init event, exactly one
//     assistant message, and exactly one terminal result event;
//   - ignored: any other event type, anywhere after system/init, because it
//     carries no report content and its presence is a transport detail of the
//     installed build;
//   - rejected: tool use in the assistant message, a second assistant message
//     or a second result, num_turns other than 1, a missing terminal result,
//     and a result whose is_error is true.
//
// Fields the parser does not read are not constrained, which is the other
// deliberate difference from Pi: Pi's events are pinned to one build and
// validated field-exact, while these events are large, build-dependent, and
// would break on an unrelated addition.
//
// The terminal text is result.result, not the assistant text. The assistant
// event is where a tool call would appear, so it is inspected for that and
// nothing else.
func parseClaudeReportStream(data []byte) (string, *ReportNormalizationError) {
	if len(data) == 0 {
		return "", &ReportNormalizationError{reportStageStream, reportReasonEmpty}
	}
	if data[len(data)-1] != '\n' {
		return "", &ReportNormalizationError{reportStageStream, reportReasonUnterminated}
	}
	if !utf8.Valid(data) {
		return "", &ReportNormalizationError{reportStageStream, reportReasonInvalidUTF8}
	}
	report := ""
	initialized, assistantSeen, resultSeen := false, false, false
	for index, line := range splitStreamLines(data) {
		event, ok := responseObject(line)
		if !ok {
			return "", &ReportNormalizationError{reportStageStream, reportReasonMalformedRecord}
		}
		kind, ok := stringField(event, "type")
		if !ok {
			return "", &ReportNormalizationError{reportStageEnvelope, reportReasonMissingType}
		}
		switch {
		case kind == "system":
			subtype, ok := stringField(event, "subtype")
			if index != 0 || initialized || !ok || subtype != "init" {
				return "", &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			initialized = true
		case !initialized:
			// Nothing precedes system/init, not even an ignored event: without
			// it there is no evidence the run started under the neutralized
			// configuration this adapter asked for.
			return "", &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
		case kind == "assistant":
			if resultSeen {
				return "", &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			if assistantSeen {
				return "", &ReportNormalizationError{reportStageMessage, reportReasonExtraMessages}
			}
			assistantSeen = true
			if diagnostic := claudeAssistantMessage(event); diagnostic != nil {
				return "", diagnostic
			}
		case kind == "result":
			if !assistantSeen {
				return "", &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			if resultSeen {
				return "", &ReportNormalizationError{reportStageResult, reportReasonExtraMessages}
			}
			resultSeen = true
			text, diagnostic := claudeTerminalResult(event)
			if diagnostic != nil {
				return "", diagnostic
			}
			report = text
		}
	}
	if !resultSeen {
		return "", &ReportNormalizationError{reportStageStream, reportReasonIncomplete}
	}
	return report, nil
}

// claudeAssistantMessage validates the one assistant message of a single-turn
// run. It extracts no text: the terminal text comes from the result event. It
// exists to prove the turn stayed tool-free, which is a property of the report
// and not of the transport.
func claudeAssistantMessage(event map[string]json.RawMessage) *ReportNormalizationError {
	var message map[string]json.RawMessage
	if json.Unmarshal(event["message"], &message) != nil {
		return &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	role, ok := stringField(message, "role")
	var content []json.RawMessage
	if !ok || role != "assistant" || json.Unmarshal(message["content"], &content) != nil || len(content) == 0 {
		return &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	for _, raw := range content {
		var block map[string]json.RawMessage
		if json.Unmarshal(raw, &block) != nil {
			return &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
		}
		kind, ok := stringField(block, "type")
		if !ok {
			return &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
		}
		switch kind {
		case "tool_use", "server_tool_use", "tool_result", "mcp_tool_use", "mcp_tool_result":
			return &ReportNormalizationError{reportStageMessage, reportReasonToolCall}
		case "thinking", "redacted_thinking":
			// A thinking block is validated as a known block and discarded; it
			// is never surfaced as report content.
		case "text":
			if !stringFieldPresent(block, "text") {
				return &ReportNormalizationError{reportStageMessage, reportReasonInvalidText}
			}
		default:
			return &ReportNormalizationError{reportStageMessage, reportReasonInvalidText}
		}
	}
	return nil
}

// claudeTerminalResult validates the terminal result event and returns the
// report text it carries.
//
// is_error is the failure signal, and it is not a parse rejection: an
// unauthenticated Claude Code emits a perfectly well-formed result with
// is_error true and terminal_reason "api_error", which is the backend telling
// us it is unavailable. It is named as such so a consumer sees a typed
// availability failure and never a silent fallback to another backend.
func claudeTerminalResult(event map[string]json.RawMessage) (string, *ReportNormalizationError) {
	var turns *float64
	if json.Unmarshal(event["num_turns"], &turns) != nil || turns == nil || *turns != 1 {
		return "", &ReportNormalizationError{reportStageResult, reportReasonTurnMismatch}
	}
	var failed *bool
	if json.Unmarshal(event["is_error"], &failed) != nil || failed == nil {
		return "", &ReportNormalizationError{reportStageResult, reportReasonInvalidMessage}
	}
	if *failed {
		reason, _ := stringField(event, "terminal_reason")
		if reason == claudeAPIErrorReason {
			return "", &ReportNormalizationError{reportStageResult, reportReasonBackendUnavailable}
		}
		return "", &ReportNormalizationError{reportStageResult, reportReasonBackendFailed}
	}
	text, ok := stringField(event, "result")
	if !ok {
		return "", &ReportNormalizationError{reportStageResult, reportReasonInvalidText}
	}
	return text, nil
}

// claudeAPIErrorReason is the terminal_reason Claude Code reports when the
// turn never reached the model, which is what an unauthenticated install
// produces.
const claudeAPIErrorReason = "api_error"

// stringFieldPresent reports whether a field decodes as a string. Unlike
// stringFieldOK it accepts the empty string, because an empty text block is
// well-formed transport and emptiness is judged on the assembled report.
func stringFieldPresent(object map[string]json.RawMessage, field string) bool {
	raw, present := object[field]
	if !present {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil
}

// splitStreamLines splits one newline-terminated JSON Lines stream into its
// events. The caller has already rejected an unterminated stream, so the
// trailing newline is dropped rather than yielding a trailing empty event.
func splitStreamLines(data []byte) [][]byte {
	return bytes.Split(data[:len(data)-1], []byte("\n"))
}

// claudeAuthArgv is the exact token contract for Claude Code's availability
// probe. `claude auth status --json` is the cheapest honest answer this CLI
// offers: it reads the installed credential state and prints it, without
// opening a session, contacting a provider, or spending a turn.
//
// It was established by capture rather than by reading terminal_reason out of
// a failed run, because a failed run is not evidence of unavailability — it is
// evidence that one run failed. Both states were observed on claude 2.1.278:
// with the operator's own configuration the command exits 0 and reports
// loggedIn true; with HOME redirected to an empty directory, which T3 had
// already established de-authenticates Claude Code, it exits 1 and reports
// loggedIn false. Both captures are committed under testdata/.
func claudeAuthArgv() []string {
	return []string{"auth", "status", "--json"}
}

// ProbeAvailability answers whether this Claude Code install can run a report,
// before anything is launched. There is one probe and not two: see
// ProbeClaudeAuth for why a model probe would be a question Claude Code cannot
// answer honestly.
func (claudeBackend) ProbeAvailability(ctx context.Context, bound BoundRuntime, _ qaroute.ResolvedRoute) AvailabilityVerdict {
	claude, ok := bound.(boundClaude)
	if !ok || ctx == nil || !absolutePaths(claude.executable.path, claude.cwd) {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	capture := executeProbeCommand(ctx, claude.executable.path, claude.cwd, claudeAuthArgv(), maxAuthOutputBytes, maxAuthOutputBytes)
	auth := ProbeClaudeAuth(BackendProbeInput{Stdout: capture.stdout, ExitCode: capture.exitCode, Complete: capture.complete()})
	if !auth.Ready {
		return AvailabilityVerdict{Code: auth.Code}
	}
	return AvailabilityVerdict{Available: true}
}
