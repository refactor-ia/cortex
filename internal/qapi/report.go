package qapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// reportRequirement is the requirements-analyst report-mode result contract:
// a substantive plain-text report, never an admission identity echo. It is
// preserved byte-for-byte; the other resolved roles carry their own result
// contracts below.
const reportRequirement = "Return one plain-text requirements analysis report. Quote every supplied requirement verbatim, flag every inconsistency explicitly, and request an explicit resolution for each conflict without inventing policy. Do not echo identity facts and do not call tools."

// Role-specific report-mode result contracts. Every new instruction keeps the
// shared prohibitions against identity echoes, tool calls, and unsupported
// execution claims, without imposing requirements-analysis directives on
// unrelated roles.
const (
	reportTestDesignerInstruction      = "Return one plain-text test design report. Propose test conditions and coverage rationale within the supplied scope without inventing requirements. Do not echo identity facts, do not call tools, and do not claim execution you did not perform."
	reportExploratoryTesterInstruction = "Return one plain-text exploratory assessment report. Assess the supplied behavior evidence, keep recorded observations separate from proposed checks, and never invent observations or claim live investigation. Do not echo identity facts, do not call tools, and do not claim execution you did not perform."
	reportAdversarialTesterInstruction = "Return one plain-text adversarial review report. Examine assumptions, boundaries, and failure behavior, keeping every hypothesis explicitly distinguished from observed findings without inventing findings. Do not echo identity facts, do not call tools, and do not claim execution you did not perform."
	reportTestRunnerInstruction        = "Return one plain-text test assessment report. Assess the supplied test evidence with explicit attribution and uncertainty; never claim to have run tests, and when evidence is missing report that the outcome cannot be determined rather than inventing pass or fail. Do not echo identity facts, do not call tools, and do not claim execution you did not perform."
	reportEvidenceAuditorInstruction   = "Return one plain-text evidence audit report. Assess the sufficiency, attribution, and uncertainty of the supplied evidence without fabricating evidence or conclusions. Do not echo identity facts, do not call tools, and do not claim execution you did not perform."
)

// reportInstruction selects the report-mode result contract for one resolved
// role. It fails closed on any unsupported role: callers never emit an empty
// instruction and never fall back to another role's contract. The selected
// instruction drives both the request size bound and the emitted result
// section.
func reportInstruction(role qarole.RoleID) (string, bool) {
	switch role {
	case qarole.RequirementsAnalyst:
		return reportRequirement, true
	case qarole.TestDesigner:
		return reportTestDesignerInstruction, true
	case qarole.ExploratoryTester:
		return reportExploratoryTesterInstruction, true
	case qarole.AdversarialTester:
		return reportAdversarialTesterInstruction, true
	case qarole.TestRunner:
		return reportTestRunnerInstruction, true
	case qarole.EvidenceAuditor:
		return reportEvidenceAuditorInstruction, true
	default:
		return "", false
	}
}

// ReportRequest is one closed local QA report request. The catalog and install
// roots are explicit caller inputs; Cortex never infers them from ambient state.
type ReportRequest struct {
	Role             qarole.RoleID
	CatalogRoot      string
	InstallRoot      string
	CurrentDirectory string
	Task             []byte
	TimeoutSeconds   int
}

// RunLocalReport runs one bounded local QA report for an installed role on Pi.
// It reuses the admission asset identity chain, route resolution, invocation
// runner, and runtime bounds, but returns the substantive report text and never
// an admission receipt or an admitted claim.
func RunLocalReport(ctx context.Context, request ReportRequest, pi PathResolver) (string, qaadmission.Code, error) {
	return runLocalReport(ctx, request, NewPiBackend(pi))
}

// RunLocalReportOn runs one bounded local QA report on the named backend. It
// is RunLocalReport with the backend chosen by the caller instead of fixed,
// and it is the entry point a caller that can select a runtime uses.
//
// The backend token must be one the route policy admits; an unadmitted token
// is refused here, before any catalog, asset, or runtime work, and never falls
// back to another backend. That refusal is the same typed code an unsupported
// runtime reaches, because from the caller's side they are the same failure:
// the requested backend cannot run this report.
func RunLocalReportOn(ctx context.Context, request ReportRequest, id string, resolver PathResolver) (string, qaadmission.Code, error) {
	backend, ok := NewBackend(id, resolver)
	if !ok {
		return "", qaadmission.CodeUnsupportedRuntime, fmt.Errorf("report backend is not admitted")
	}
	return runLocalReport(ctx, request, backend)
}

// runLocalReport sequences one report run against any backend. Everything that
// makes the output evidence rather than opinion lives here and not in the
// adapter: the route policy decides the backend is admissible, the catalog
// admission binding and installed assets are verified before anything is
// launched, the process runs once under shared bounds, and every terminal
// condition maps to a fixed admission code. A backend contributes only its
// identity, its binding, its argv, its input frame, and its stream parser.
func runLocalReport(ctx context.Context, request ReportRequest, backend Backend) (string, qaadmission.Code, error) {
	route, failure := qaroute.Resolve(qaroute.Request{Role: request.Role, Backend: backend.ID()}, qaroute.Snapshot{})
	if failure.Code != "" {
		return "", qaadmission.Code(failure.Code), fmt.Errorf("report route: %s", failure.Code)
	}
	snapshot, err := catalog.BuildCatalogSnapshot(request.CatalogRoot, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		return "", qaadmission.CodeAdapterUnavailable, err
	}
	expected, err := CatalogAdmissionBinding(snapshot, request.Role, backend.ID())
	if err != nil {
		return "", qaadmission.CodeAdapterUnavailable, err
	}
	assets, err := installobserve.ObserveAdmissionAssets(request.InstallRoot, request.CurrentDirectory, expected)
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	// Binding and availability probing are preflight, and they are charged to
	// their own budget rather than to the run's; see preflightContext.
	preflight, endPreflight := preflightContext(ctx)
	bound, err := backend.Bind(preflight, request.CurrentDirectory)
	if err != nil {
		return "", qaadmission.CodeUnsupportedRuntime, err
	}
	paths, err := BindInvocationPaths(request.Role, bound.Path(), assets.ActorPath(), assets.SkillPath(), bound.CWD())
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	invocation, err := backend.BuildInvocation(route, paths)
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	frame, err := backend.EncodeInput(route, assets.ActorSHA256(), assets.SkillSHA256(), request.Task)
	if err != nil {
		return "", qaadmission.CodeInvalidRequest, err
	}
	// Availability is asked out of band and before launch, never inferred from
	// the run's own stream. A backend that cannot answer at all therefore
	// fails here, with its own typed code, and its stdout never reaches the
	// parser. That is what keeps the terminal codes below narrow: they mean
	// the backend was available, ran, and the run itself failed.
	verdict := backend.ProbeAvailability(preflight, bound, route)
	spent := endPreflight()
	if !verdict.Available {
		return "", verdict.Code, fmt.Errorf("report backend is unavailable")
	}
	run, cancelRun := runBudget(ctx, spent)
	defer cancelRun()
	// A run with no budget left must not be launched and then read as a
	// format failure. Both ways that happens are named here: the caller's
	// deadline is a timeout and the caller's cancellation is a failed run.
	// Neither is a malformed stream, and neither may reach the parser.
	if err := run.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", qaadmission.CodeExecutionTimedOut, nil
		}
		return "", qaadmission.CodeExecutionFailed, fmt.Errorf("report run was cancelled before launch")
	}
	facts := runOnce(run, invocation, frame, time.Duration(request.TimeoutSeconds)*time.Second)
	switch {
	case facts.invalid:
		return "", qaadmission.CodeInvalidRequest, fmt.Errorf("invalid report run")
	case facts.launchFailed:
		return "", qaadmission.CodeLaunchFailed, nil
	case facts.timedOut:
		return "", qaadmission.CodeExecutionTimedOut, nil
	case facts.exitNonZero || facts.waitFailed:
		return "", qaadmission.CodeExecutionFailed, nil
	case facts.stdoutTruncated || facts.stderrTruncated:
		return "", qaadmission.CodeOutputTruncated, nil
	case len(facts.stdout) == 0:
		return "", qaadmission.CodeOutputSilent, nil
	}
	report, diagnostic := backend.ParseReport(facts.stdout)
	if diagnostic != nil {
		return "", reportFailureCode(diagnostic), diagnostic
	}
	if strings.TrimSpace(report) == "" {
		return "", qaadmission.CodeNormalizationFailed, &ReportNormalizationError{reportStageReport, reportReasonBlank}
	}
	return report, "", nil
}

// preflightBudget bounds the whole pre-launch sequence of one report run:
// binding the runtime and probing its availability. Each step already bounds
// its own child process (versionProbeTimeout, availabilityProbeTimeout); this
// is the ceiling on the sequence, so preflight cannot outlive a plausible run
// even when every step is slow.
const preflightBudget = 45 * time.Second

// preflightContext detaches preflight from the report run's execution budget
// and returns the time preflight spent.
//
// Binding and probing each spawn their own child process. Charging them to the
// caller's deadline means that on a slow machine a bounded report run can
// spend its budget before the run starts — and what the consumer sees then is
// not a deadline. The run is cancelled before or during launch, its stream is
// empty or its probe capture is incomplete, and an incomplete capture is
// reported as normalization_failed: the output format blamed for a budget that
// was already gone. Preflight is not the run, so it does not spend the run's
// budget; the run keeps the whole budget the caller granted it.
//
// The returned context keeps the caller's explicit cancellation and drops only
// the caller's deadline. A maintainer's interrupt must still stop preflight.
func preflightContext(ctx context.Context) (context.Context, func() time.Duration) {
	started := time.Now()
	preflight, cancel := context.WithTimeout(context.WithoutCancel(ctx), preflightBudget)
	stop := forwardCancellation(ctx, cancel)
	return preflight, func() time.Duration {
		stop()
		cancel()
		return time.Since(started)
	}
}

// runBudget returns the context the report run executes under: the caller's
// context with its deadline pushed out by the time preflight spent, so the run
// is bounded by the budget the caller granted rather than by its remainder. A
// caller with no deadline has nothing to restore.
func runBudget(ctx context.Context, spent time.Duration) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || spent <= 0 {
		return ctx, func() {}
	}
	run, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(spent))
	stop := forwardCancellation(ctx, cancel)
	return run, func() {
		stop()
		cancel()
	}
}

// forwardCancellation propagates only ctx's explicit cancellation to a derived
// context built with context.WithoutCancel. The standard library offers no
// derivation that keeps cancellation while dropping the deadline, so the
// cancellation half is re-attached here and the deadline half is not.
func forwardCancellation(ctx context.Context, cancel context.CancelFunc) func() bool {
	return context.AfterFunc(ctx, func() {
		if errors.Is(ctx.Err(), context.Canceled) {
			cancel()
		}
	})
}

// reportFailureCode maps one parser diagnostic onto its terminal admission
// code. A malformed stream is a normalization failure; a well-formed stream in
// which the backend reported its own terminal failure is not, and calling it
// one is what T3 flagged.
//
// Both typed backend reasons land on CodeExecutionFailed, and the collapse is
// deliberate. Availability is settled before launch by ProbeAvailability, so a
// backend that reaches the parser at all was available: a terminal failure it
// reports afterwards is a run that failed, not an install that is unusable.
// CodeAuthNotReady and CodeModelUnavailable keep one producer each — the
// pre-run probe — instead of two that can disagree.
func reportFailureCode(diagnostic *ReportNormalizationError) qaadmission.Code {
	switch diagnostic.Reason {
	case reportReasonBackendUnavailable, reportReasonBackendFailed:
		return qaadmission.CodeExecutionFailed
	default:
		return qaadmission.CodeNormalizationFailed
	}
}

// ReportNormalizationError is the bounded diagnostic for one rejected report
// stream. Stage and reason come from the fixed code-owned vocabularies below
// and never carry stream content, error text, or provider output.
type ReportNormalizationError struct {
	Stage  string
	Reason string
}

func (e *ReportNormalizationError) Error() string {
	return "report normalization rejected: " + e.Stage + " " + e.Reason
}

// Fixed diagnostic vocabularies for rejected report streams. The tokens are
// stable machine constants owned by this file; they never echo stream data.
const (
	reportStageStream   = "stream"   // whole-stream transport envelope
	reportStageEnvelope = "envelope" // per-event record structure
	reportStageMessage  = "message"  // assistant message and turn content
	reportStageReport   = "report"   // extracted report text
	reportStageResult   = "result"   // terminal result event of one run
)

const (
	reportReasonEmpty           = "empty"
	reportReasonUnterminated    = "unterminated"
	reportReasonInvalidUTF8     = "invalid_utf8"
	reportReasonMalformedRecord = "malformed_record"
	reportReasonIncomplete      = "incomplete"
	reportReasonMissingType     = "missing_type"
	reportReasonUnexpected      = "unexpected_event"
	reportReasonInvalidMessage  = "invalid_message"
	reportReasonToolCall        = "tool_call"
	reportReasonInvalidText     = "invalid_text"
	reportReasonTurnMismatch    = "turn_mismatch"
	reportReasonToolResults     = "tool_results"
	reportReasonExtraMessages   = "extra_messages"
	reportReasonRetryFlag       = "retry_flag"
	reportReasonBlank           = "blank"
	// A backend that reports its own terminal failure is not a malformed
	// stream. These two reasons keep that distinction: the run was well
	// formed and the backend said it could not answer, which a consumer must
	// see as a typed availability or execution failure rather than as a
	// parser rejection, and never as a silent fallback to another backend.
	reportReasonBackendUnavailable = "backend_unavailable"
	reportReasonBackendFailed      = "backend_failed"
)

type lookPathPi struct{}

func (lookPathPi) Resolve(ctx context.Context) (string, error) {
	return exec.LookPath("pi")
}

// EncodeReportInput frames one bounded report input. Unlike EncodeInput it does
// not demand an identity echo and carries no revision or candidate claim.
func EncodeReportInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, task []byte) ([]byte, error) {
	return encodeReportInput(route, piBackendID, actorSHA256, skillSHA256, task)
}

// encodeReportInput frames one bounded report input for one backend. The frame
// itself is backend-independent — same role instruction, same identity block,
// same size bound — so a role reads the same request whichever runtime executes
// it; only the backend token the route was pinned to differs.
func encodeReportInput(route qaroute.ResolvedRoute, backendID, actorSHA256, skillSHA256 string, task []byte) ([]byte, error) {
	if !validRouteFor(route, backendID) || !validProfile(route) || !lowerSHA256(actorSHA256) || !lowerSHA256(skillSHA256) || !validTask(task) {
		return nil, fmt.Errorf("invalid report input")
	}
	instruction, ok := reportInstruction(route.Role)
	if !ok {
		return nil, fmt.Errorf("invalid report input")
	}
	identity := []byte(strings.Join([]string{
		"input_contract " + InputContractFor(backendID),
		"role " + string(route.Role),
		"actor_contract " + qaactor.ActorContractVersion,
		"actor_sha256 " + actorSHA256,
		"skill_contract " + skillContractFor(backendID),
		"skill_sha256 " + skillSHA256,
		"route_policy " + route.PolicyVersion,
		"route_backend " + route.Backend,
		"route_provider " + route.Provider,
		"route_model " + route.Model,
		"route_effort " + route.Effort,
		"route_profile " + route.ProfileID,
		"route_profile_sha256 " + route.ProfileSHA256,
		"route_override_fields " + strings.Join(route.OverrideFields, ","),
	}, "\n"))
	size := len("/skill:cortex-") + len(route.Role) + 1 + sectionSize("identity", identity) + sectionSize("task", task) + sectionSize("result", []byte(instruction))
	if size > qaadmission.MaxRequestBytes {
		return nil, fmt.Errorf("report input exceeds bound")
	}
	frame := make([]byte, 0, size)
	frame = append(frame, "/skill:cortex-"...)
	frame = append(frame, string(route.Role)...)
	frame = append(frame, '\n')
	frame = appendSection(frame, "identity", identity)
	frame = appendSection(frame, "task", task)
	return appendSection(frame, "result", []byte(instruction)), nil
}

// parseReportStream extracts one substantive report from a Pi JSON event
// stream. Tool calls, duplicate results, and transport noise fail closed. On
// rejection it reports which fixed parser condition failed.
func parseReportStream(data []byte) (string, bool, *ReportNormalizationError) {
	if len(data) == 0 {
		return "", false, &ReportNormalizationError{reportStageStream, reportReasonEmpty}
	}
	if data[len(data)-1] != '\n' {
		return "", false, &ReportNormalizationError{reportStageStream, reportReasonUnterminated}
	}
	if !utf8.Valid(data) {
		return "", false, &ReportNormalizationError{reportStageStream, reportReasonInvalidUTF8}
	}
	state, session, report := 0, false, ""
	echoSeen, echoStart, terminal := false, json.RawMessage(nil), json.RawMessage(nil)
	for _, line := range bytes.Split(data[:len(data)-1], []byte("\n")) {
		event, ok := responseObject(line)
		if !ok {
			return "", false, &ReportNormalizationError{reportStageStream, reportReasonMalformedRecord}
		}
		kind, ok := stringField(event, "type")
		if !ok {
			return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonMissingType}
		}
		switch state {
		case 0:
			if kind == "session" && !session && exactFields(event, "type", "version", "id", "timestamp", "cwd") && versionField(event) && stringFieldOK(event, "id") && stringFieldOK(event, "timestamp") && stringFieldOK(event, "cwd") {
				session = true
			} else if kind == "agent_start" && exactFields(event, "type") {
				state = 1
			} else {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
		case 1:
			if kind != "turn_start" || !exactFields(event, "type") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			state = 2
		case 2:
			if kind != "message_start" || !exactFields(event, "type", "message") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			// runAgentLoop re-emits each input prompt as a matched
			// message_start/message_end pair before assistant generation;
			// accept at most one strictly validated initial prompt echo.
			if !echoSeen && validUserEcho(event["message"]) {
				echoSeen = true
				echoStart = event["message"]
				state = 8
			} else if !validAssistant(event["message"], false) {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
			} else {
				state = 3
			}
		case 8:
			if kind != "message_end" || !exactFields(event, "type", "message") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			if !bytes.Equal(echoStart, event["message"]) {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
			}
			state = 2
		case 3:
			if kind == "message_update" {
				if !validUpdate(event) {
					return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
				}
				continue
			}
			if kind != "message_end" || !exactFields(event, "type", "message") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			text, ok, diagnostic := reportText(event)
			if !ok {
				return "", false, diagnostic
			}
			report = text
			terminal = event["message"]
			state = 4
		case 4:
			turn, valid, diagnostic := reportText(event)
			if kind != "turn_end" || !exactFields(event, "type", "message", "toolResults") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			if !valid {
				return "", false, diagnostic
			}
			if turn != report {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonTurnMismatch}
			}
			var tools []json.RawMessage
			if json.Unmarshal(event["toolResults"], &tools) != nil || len(tools) != 0 {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonToolResults}
			}
			state = 5
		case 5:
			if kind != "agent_end" || !exactFields(event, "type", "messages", "willRetry") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			// runAgentLoop reports newMessages: the input prompts it echoed
			// followed by the terminal assistant message, each serialized from
			// the same object as its message_end event, so the array must be
			// exactly [echo?, terminal] byte-for-byte; anything else is extra.
			var messages []json.RawMessage
			expected := []json.RawMessage{terminal}
			if echoSeen {
				expected = append([]json.RawMessage{echoStart}, expected...)
			}
			if json.Unmarshal(event["messages"], &messages) != nil || len(messages) != len(expected) {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonExtraMessages}
			}
			for index := range expected {
				if !bytes.Equal(messages[index], expected[index]) {
					return "", false, &ReportNormalizationError{reportStageMessage, reportReasonExtraMessages}
				}
			}
			if !falseField(event, "willRetry") {
				return "", false, &ReportNormalizationError{reportStageMessage, reportReasonRetryFlag}
			}
			state = 6
		case 6:
			if kind != "agent_settled" || !exactFields(event, "type") {
				return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
			}
			state = 7
		default:
			return "", false, &ReportNormalizationError{reportStageEnvelope, reportReasonUnexpected}
		}
	}
	if state == 6 || state == 7 {
		return report, true, nil
	}
	return "", false, &ReportNormalizationError{reportStageStream, reportReasonIncomplete}
}

func reportText(event map[string]json.RawMessage) (string, bool, *ReportNormalizationError) {
	var message map[string]json.RawMessage
	if json.Unmarshal(event["message"], &message) != nil || !allowedFields(message, "role", "content", "api", "provider", "model", "responseModel", "responseId", "providerThinkingLevel", "diagnostics", "usage", "stopReason", "deferred", "errorMessage", "rawStopReason", "endTurn", "timestamp") {
		return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	role, ok := stringField(message, "role")
	var content []json.RawMessage
	if !ok || role != "assistant" || json.Unmarshal(message["content"], &content) != nil {
		return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	// Reasoning models emit one leading thinking block before the report text
	// when the route enables thinking; the thinking block is validated and
	// discarded, never surfaced as report content.
	if len(content) == 2 && thinkingContent(content[0]) {
		content = content[1:]
	}
	if len(content) != 1 {
		return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidMessage}
	}
	if toolContent(content[0]) {
		return "", false, &ReportNormalizationError{reportStageMessage, reportReasonToolCall}
	}
	text, ok := textContent(content[0])
	if !ok {
		return "", false, &ReportNormalizationError{reportStageMessage, reportReasonInvalidText}
	}
	return text, true, nil
}

// thinkingContent validates the source-derived thinking block shape: exactly
// {type: "thinking", thinking} with an optional provider thinkingSignature.
func thinkingContent(raw json.RawMessage) bool {
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil || !exactFields(block, "type", "thinking") && !exactFields(block, "type", "thinking", "thinkingSignature") {
		return false
	}
	kind, ok := stringField(block, "type")
	return ok && kind == "thinking" && stringFieldOK(block, "thinking")
}

// validUserEcho validates the source-derived prompt echo shape: pi constructs
// one text input as exactly {role, content, timestamp} with a single text
// block, and runAgentLoop serializes the same message object into both events
// of the pair, so start and end must be byte-identical.
func validUserEcho(raw json.RawMessage) bool {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || !exactFields(message, "role", "content", "timestamp") {
		return false
	}
	role, ok := stringField(message, "role")
	var content []json.RawMessage
	if !ok || role != "user" || !numberField(message, "timestamp") || json.Unmarshal(message["content"], &content) != nil || len(content) != 1 {
		return false
	}
	text, ok := textContent(content[0])
	return ok && text != ""
}
