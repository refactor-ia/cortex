package qapi

import (
	"bytes"
	"context"
	"encoding/json"
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

// reportRequirement is the report-mode result contract: a substantive
// plain-text report, never an admission identity echo.
const reportRequirement = "Return one plain-text requirements analysis report. Quote every supplied requirement verbatim, flag every inconsistency explicitly, and request an explicit resolution for each conflict without inventing policy. Do not echo identity facts and do not call tools."

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

// RunLocalReport runs one bounded local QA report for an installed role. It
// reuses the admission asset identity chain, route resolution, invocation
// runner, and runtime bounds, but returns the substantive report text and never
// an admission receipt or an admitted claim.
func RunLocalReport(ctx context.Context, request ReportRequest, pi PiPathResolver) (string, qaadmission.Code, error) {
	if pi == nil {
		pi = lookPathPi{}
	}
	route, failure := qaroute.Resolve(qaroute.Request{Role: request.Role, Backend: "pi"}, qaroute.Snapshot{})
	if failure.Code != "" {
		return "", qaadmission.Code(failure.Code), fmt.Errorf("report route: %s", failure.Code)
	}
	snapshot, err := catalog.BuildCatalogSnapshot(request.CatalogRoot, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		return "", qaadmission.CodeAdapterUnavailable, err
	}
	expected, err := CatalogAdmissionBinding(snapshot, request.Role, "pi")
	if err != nil {
		return "", qaadmission.CodeAdapterUnavailable, err
	}
	assets, err := installobserve.ObserveAdmissionAssets(request.InstallRoot, request.CurrentDirectory, expected)
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	bound, err := bindPi(ctx, request.CurrentDirectory, pi)
	if err != nil {
		return "", qaadmission.CodeUnsupportedRuntime, err
	}
	paths, err := BindInvocationPaths(request.Role, bound.path, assets.ActorPath(), assets.SkillPath(), bound.cwd)
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	invocation, err := BuildInvocation(route, paths)
	if err != nil {
		return "", qaadmission.CodeActorUnavailable, err
	}
	frame, err := EncodeReportInput(route, assets.ActorSHA256(), assets.SkillSHA256(), request.Task)
	if err != nil {
		return "", qaadmission.CodeInvalidRequest, err
	}
	facts := runOnce(ctx, invocation, frame, time.Duration(request.TimeoutSeconds)*time.Second)
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
	report, ok, diagnostic := parseReportStream(facts.stdout)
	if !ok {
		return "", qaadmission.CodeNormalizationFailed, diagnostic
	}
	if strings.TrimSpace(report) == "" {
		return "", qaadmission.CodeNormalizationFailed, &ReportNormalizationError{reportStageReport, reportReasonBlank}
	}
	return report, "", nil
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
)

type lookPathPi struct{}

func (lookPathPi) ResolvePi(ctx context.Context) (string, error) {
	return exec.LookPath("pi")
}

// EncodeReportInput frames one bounded report input. Unlike EncodeInput it does
// not demand an identity echo and carries no revision or candidate claim.
func EncodeReportInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, task []byte) ([]byte, error) {
	if !validRoute(route) || !validProfile(route) || !lowerSHA256(actorSHA256) || !lowerSHA256(skillSHA256) || !validTask(task) {
		return nil, fmt.Errorf("invalid Pi report input")
	}
	identity := []byte(strings.Join([]string{
		"input_contract " + InputContract,
		"role " + string(route.Role),
		"actor_contract " + qaactor.ActorContractVersion,
		"actor_sha256 " + actorSHA256,
		"skill_contract " + skillContract,
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
	size := len("/skill:cortex-") + len(route.Role) + 1 + sectionSize("identity", identity) + sectionSize("task", task) + sectionSize("result", []byte(reportRequirement))
	if size > qaadmission.MaxRequestBytes {
		return nil, fmt.Errorf("Pi report input exceeds bound")
	}
	frame := make([]byte, 0, size)
	frame = append(frame, "/skill:cortex-"...)
	frame = append(frame, string(route.Role)...)
	frame = append(frame, '\n')
	frame = appendSection(frame, "identity", identity)
	frame = appendSection(frame, "task", task)
	return appendSection(frame, "result", []byte(reportRequirement)), nil
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
