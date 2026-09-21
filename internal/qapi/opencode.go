package qapi

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// opencodeBackendID is the policy token for the OpenCode backend. It must
// match the backend the route policy admits; qaroute owns that set, and until
// it admits this token no route resolves and nothing here can be reached.
const opencodeBackendID = "opencode"

// opencodeVersionOutput matches the fixed `opencode --version` line. Unlike
// Pi ("pi 0.85.1") and Claude Code ("2.1.278 (Claude Code)"), OpenCode prints
// a bare semantic version with no product name, so the contract is the bare
// form and the other two runtimes' lines bind nothing here.
var opencodeVersionOutput = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)$`)

// opencodeBackend runs one QA role on OpenCode's headless `run` mode. It is
// the third Backend implementation and, like the Claude Code adapter, shares
// none of Pi's stream assumptions: only the port, the process bounds, and the
// input frame are common.
type opencodeBackend struct {
	resolver PathResolver
}

// NewOpenCodeBackend registers OpenCode as a QA backend. A nil resolver
// selects the production constrained PATH lookup, which is the only ambient
// state this package consults and only when the caller supplied no candidate.
func NewOpenCodeBackend(resolver PathResolver) Backend {
	if resolver == nil {
		resolver = lookPathOpenCode{}
	}
	return opencodeBackend{resolver: resolver}
}

type lookPathOpenCode struct{}

func (lookPathOpenCode) Resolve(ctx context.Context) (string, error) {
	return exec.LookPath("opencode")
}

func (opencodeBackend) ID() string {
	return opencodeBackendID
}

func (backend opencodeBackend) Bind(ctx context.Context, cwd string) (BoundRuntime, error) {
	bound, err := bindOpenCode(ctx, cwd, backend.resolver)
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
// As with Claude Code, the actor and skill paths do not appear in argv:
// OpenCode's `--agent` selects an agent from the operator's own configuration,
// which is precisely the ambient state this adapter is neutralizing, and there
// is no flag that takes an actor file. The role, the actor digest, and the
// skill digest reach the process through the bounded stdin frame instead.
//
// The binding therefore carries no actor path at all: install ships this
// runtime no actor file, because nothing here would read one. The installed
// skill is still observed and digest-verified before anything is launched,
// and the actor is attested from catalog provenance — the same bytes the
// frame carries.
func (opencodeBackend) BuildInvocation(route qaroute.ResolvedRoute, binding BoundInvocationPaths) (Invocation, error) {
	if !validRouteFor(route, opencodeBackendID) || route.Role != binding.role || !absolutePaths(binding.binary, binding.skill, binding.cwd) {
		return Invocation{}, errors.New("invalid OpenCode invocation binding")
	}
	return Invocation{binary: binding.binary, cwd: binding.cwd, argv: openCodeArgv(route)}, nil
}

// openCodeArgv is the exact, input-independent token contract for one headless
// OpenCode run. Every token below is load-bearing and must not be
// "simplified" away.
//
// --pure is the isolation token. A headless OpenCode run is not isolated by
// default: without it the operator's external plugins load and run. Observed
// against opencode 1.18.25 — a default run emitted the operator's
// "[skill-registry] skipping refresh" plugin lines on stderr and reported
// 22516 input tokens for a one-word prompt; the same run with --pure emitted
// nothing on stderr and reported 21599, so roughly 900 tokens of operator
// plugin context were reaching the model and deciding what the report says.
// That is exactly the property this vertical exists to deny.
//
// --format json selects the machine stream. Despite the name the output is
// JSON Lines, one event object per line, which is what the parser reads.
//
// The role input is not one of these tokens. `opencode run` accepts the
// message as a positional argument but also reads it from stdin, and stdin is
// what this adapter uses: it keeps the bounded-input property the shared
// runner already enforces, it cannot hit an argv size limit on a large task,
// and it keeps role content out of the process table. Verified against
// opencode 1.18.25: piping the prompt with no positional argument produced the
// same step_start -> text -> step_finish stream.
//
// -m carries the resolved route's own provider and model, in the
// provider/model form OpenCode's model listing prints and its availability
// probe reads. It is the reason this backend's receipt records a routed model
// relationship rather than a runtime-fixed one: what answers is what policy
// chose, not whatever the operator last selected. Without it OpenCode would
// fall back to the operator's configured default, which is exactly the ambient
// state the rest of this argv neutralizes. It is passed only now that route
// policy admits this backend; before that a model flag had no admitted route
// to carry and nothing to test it against.
//
// What is absent is as deliberate as what is present. No -c/--continue,
// -s/--session or --fork, so every run opens a fresh session and can never
// resume or inherit one. No --share, so no session leaves the machine. No
// --auto, so a tool call cannot be auto-approved into execution. No --agent,
// because it would select an agent from the operator's own configuration
// rather than from the resolved route.
//
// One isolation gap is recorded rather than worked around: OpenCode still
// loads the project context files of its working directory and there is no
// flag that disables them. Observed on the same build, --pure in this
// repository reported 22575 input tokens against 21599 in an empty directory,
// the difference being the repository's own AGENTS.md. The working directory
// is the directory under review, so its instructions reach the role. This is
// the OpenCode equivalent of an unresolved question and belongs to the
// end-to-end observation task, not to a flag invented here.
func openCodeArgv(route qaroute.ResolvedRoute) []string {
	return []string{"run", "--format", "json", "--pure", "-m", route.Provider + "/" + route.Model}
}

// EncodeInput frames one bounded report input for OpenCode. The frame is the
// shared one — same instruction, same identity block — with one runtime
// difference it cannot avoid: OpenCode answers `/skill:cortex-<role>` with a
// tool call, so this frame carries the skill's text and records that it did.
func (opencodeBackend) EncodeInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, skill, task []byte) ([]byte, error) {
	return encodeReportInput(route, opencodeBackendID, actorSHA256, skillSHA256, skill, task)
}

func (opencodeBackend) ParseReport(stdout []byte) (string, *ReportNormalizationError) {
	return parseOpenCodeReportStream(stdout)
}

// boundOpenCode is one verified OpenCode binding. It carries the same file
// identity the Pi and Claude Code bindings do — so the executable cannot change
// between the version probe and the run — plus the observed version.
type boundOpenCode struct {
	executable     executableIdentity
	cwd, version   string
	versionCapture versionCapture
}

func (bound boundOpenCode) Path() string {
	return bound.executable.path
}

func (bound boundOpenCode) CWD() string {
	return bound.cwd
}

// bindOpenCode resolves and verifies OpenCode once for one run. Like
// bindClaude, and for the same reason, it requires a well-formed version line
// and records it but does not pin one exact version: the stream is validated
// structurally at parse time, so an exact pin would reject working
// installations for no evidence gained.
func bindOpenCode(ctx context.Context, cwd string, resolver PathResolver) (boundOpenCode, error) {
	if ctx == nil || resolver == nil {
		return boundOpenCode{}, errors.New("OpenCode binding dependencies are unavailable")
	}
	path, err := resolver.Resolve(ctx)
	if err != nil {
		return boundOpenCode{}, errors.New("OpenCode resolution failed")
	}
	canonicalCWD, err := canonicalRuntimeDirectory(cwd)
	if err != nil {
		return boundOpenCode{}, err
	}
	executable, err := inspectExecutable(path)
	if err != nil {
		return boundOpenCode{}, err
	}
	capture := executeVersionCommand(ctx, executable.path, canonicalCWD)
	version, valid := parseVersionWith(capture, opencodeVersionOutput)
	if !valid {
		return boundOpenCode{}, errors.New("OpenCode version is unsupported")
	}
	after, err := inspectExecutable(executable.path)
	if err != nil || !sameExecutable(executable, after) {
		return boundOpenCode{}, errors.New("OpenCode changed during version binding")
	}
	return boundOpenCode{executable: executable, cwd: canonicalCWD, version: version, versionCapture: capture}, nil
}

// opencodeStopReason is the step_finish reason of a turn that ended because
// the model finished answering. Any other reason — "tool-calls" above all —
// means the run did not end as one completed tool-free turn.
const opencodeStopReason = "stop"

// parseOpenCodeReportStream extracts one substantive report from an OpenCode
// `run --format json` event stream. On rejection it reports which fixed parser
// condition failed and never echoes stream content.
//
// Like the Claude Code parser and unlike Pi's, it is not a strict state
// machine. The same emitter also produces reasoning, file, patch and agent
// events depending on the build and the run, none of which carry report
// content, so assuming a fixed sequence would reject a healthy run. The parser
// fixes what it requires and ignores the rest:
//
//   - required, in this order: a leading step_start event, exactly one text
//     event, and exactly one terminal step_finish event whose reason is "stop";
//   - ignored: any other event type, anywhere after step_start, because it
//     carries no report content and its presence is a transport detail of the
//     installed build;
//   - rejected: a tool_use event, a second text event, a second step_start or
//     step_finish, a missing step_finish, a step_finish that did not stop, and
//     an error event.
//
// Two shapes differ from both other backends and drive the structure here.
// First, there is no terminal result field: OpenCode carries the report text on
// the text event as part.text, and completion is a separate step_finish event,
// so the text is collected mid-stream and only released once the terminal event
// has been seen. Second, OpenCode reports a provider, credential or model
// failure as one error event carrying a name and an opaque ref, and it does not
// distinguish "not authenticated" from any other provider failure the way
// Claude Code's terminal_reason does. The failure is still typed and never
// silent; naming it more precisely than backend_failed would be an invention,
// and mapping it onto an admission code is the receipt-contract slice's work.
func parseOpenCodeReportStream(data []byte) (string, *ReportNormalizationError) {
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
	started, textSeen, finished := false, false, false
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
		case kind == "step_start":
			// A second step_start is a second step, which is a second turn:
			// the single-turn bound OpenCode offers no flag for is enforced
			// here instead.
			if index != 0 || started {
				return "", &ReportNormalizationError{reportStageResult, reportReasonTurnMismatch}
			}
			started = true
		case !started:
			// Nothing precedes step_start, not even an ignored event: without
			// it there is no evidence the run started at all. The one
			// exception is the error event below, which replaces the whole
			// stream when the run never reached the model.
			if kind == "error" {
				return "", &ReportNormalizationError{reportStageResult, reportReasonBackendFailed}
			}
			return "", &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
		case kind == "error":
			return "", &ReportNormalizationError{reportStageResult, reportReasonBackendFailed}
		case kind == "tool_use":
			// Tool-freedom is enforced here rather than by a flag: OpenCode
			// has none that disables its tools, so a turn that reached for one
			// is rejected instead of being reported as evidence.
			return "", &ReportNormalizationError{reportStageMessage, reportReasonToolCall}
		case kind == "text":
			if finished || textSeen {
				return "", &ReportNormalizationError{reportStageMessage, reportReasonExtraMessages}
			}
			textSeen = true
			text, diagnostic := openCodeText(event)
			if diagnostic != nil {
				return "", diagnostic
			}
			report = text
		case kind == "step_finish":
			if finished {
				return "", &ReportNormalizationError{reportStageResult, reportReasonTurnMismatch}
			}
			finished = true
			if diagnostic := openCodeStepFinish(event); diagnostic != nil {
				return "", diagnostic
			}
		}
	}
	if !finished {
		return "", &ReportNormalizationError{reportStageStream, reportReasonIncomplete}
	}
	if !textSeen {
		return "", &ReportNormalizationError{reportStageMessage, reportReasonIncomplete}
	}
	return report, nil
}

// openCodeText returns the report text one text event carries. An empty string
// is well-formed transport here, exactly as it is in an assistant text block;
// emptiness is judged on the assembled report.
func openCodeText(event map[string]json.RawMessage) (string, *ReportNormalizationError) {
	part, diagnostic := openCodePart(event)
	if diagnostic != nil {
		return "", diagnostic
	}
	if !stringFieldPresent(part, "text") {
		return "", &ReportNormalizationError{reportStageMessage, reportReasonInvalidText}
	}
	text, _ := stringField(part, "text")
	return text, nil
}

// openCodeStepFinish validates the terminal event of a single-turn run. It
// extracts no text — the report came from the text event — and constrains only
// the stop reason. The accompanying tokens block is accounting, and pinning
// fields the report does not depend on would break on an unrelated addition to
// a build this adapter does not pin.
func openCodeStepFinish(event map[string]json.RawMessage) *ReportNormalizationError {
	part, diagnostic := openCodePart(event)
	if diagnostic != nil {
		return diagnostic
	}
	if reason, ok := stringField(part, "reason"); !ok || reason != opencodeStopReason {
		return &ReportNormalizationError{reportStageResult, reportReasonTurnMismatch}
	}
	return nil
}

// openCodePart decodes the part object every OpenCode event carries its
// payload in.
func openCodePart(event map[string]json.RawMessage) (map[string]json.RawMessage, *ReportNormalizationError) {
	var part map[string]json.RawMessage
	if json.Unmarshal(event["part"], &part) != nil || part == nil {
		return nil, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	return part, nil
}

// openCodeModelsArgv and openCodeAuthArgv are the exact token contracts for
// OpenCode's two availability probes. Both were confirmed present on opencode
// 1.18.25 and both were captured in their available and unavailable states.
//
// `models` reads the cached model catalogue; --refresh is deliberately absent
// so the probe never reaches the network. `auth list` reads the credential
// file. --pure is on both for the same reason it is on the run itself: without
// it the operator's external plugins load and write to the stream the parser
// reads.
func openCodeModelsArgv() []string {
	return []string{"models", "--pure"}
}

func openCodeAuthArgv() []string {
	return []string{"auth", "list", "--pure"}
}

// ProbeAvailability runs OpenCode's two fixed probes in the same order Pi's
// availability probe uses: the model catalogue first, then the credentials. The
// order is not cosmetic — the model listing is the narrower question, so an
// install that cannot reach the resolved route is named CodeModelUnavailable
// rather than being reported as a credential problem it may not have.
//
// This is decision B in full: the OpenCode run stream collapses authentication
// failure and every other provider failure into one opaque envelope, so it can
// never answer "is this backend available". These two commands can, and they
// answer it before launch.
func (opencodeBackend) ProbeAvailability(ctx context.Context, bound BoundRuntime, route qaroute.ResolvedRoute) AvailabilityVerdict {
	opencode, ok := bound.(boundOpenCode)
	if !ok || ctx == nil || !absolutePaths(opencode.executable.path, opencode.cwd) {
		return AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	models := executeProbeCommand(ctx, opencode.executable.path, opencode.cwd, openCodeModelsArgv(), maxModelOutputBytes, maxAuthOutputBytes)
	model := ProbeOpenCodeModel(BackendProbeInput{Stdout: models.stdout, ExitCode: models.exitCode, Complete: models.complete()}, route.Provider, route.Model)
	if !model.Available {
		return AvailabilityVerdict{Code: model.Code}
	}
	credentials := executeProbeCommand(ctx, opencode.executable.path, opencode.cwd, openCodeAuthArgv(), maxAuthOutputBytes, maxAuthOutputBytes)
	auth := ProbeOpenCodeAuth(BackendProbeInput{Stdout: credentials.stdout, ExitCode: credentials.exitCode, Complete: credentials.complete()})
	if !auth.Ready {
		return AvailabilityVerdict{Code: auth.Code}
	}
	return AvailabilityVerdict{Available: true}
}
