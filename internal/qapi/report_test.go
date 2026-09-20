package qapi

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// reportAssistantStartLine is the assistant message_start line shared by the
// report fixtures; negative mutations anchor on it.
const reportAssistantStartLine = `{"type":"message_start","message":{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"pending"}}`

// reportUserMessage renders the source-derived user prompt shape pi emits for
// one text input: exactly {role, content, timestamp} with one text block.
func reportUserMessage(text string) string {
	encoded, err := json.Marshal(map[string]any{
		"role":      "user",
		"content":   []any{map[string]any{"type": "text", "text": text}},
		"timestamp": 0,
	})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// reportStreamFixture builds a minimal source-derived contract fixture for the
// report-mode event stream; it is trusted test evidence, not an observed
// provider capture. A non-empty prompt adds the runAgentLoop input echo pair
// (matched message_start/message_end re-emitting the prompt) after turn_start.
func reportStreamFixture(report, prompt string) []byte {
	message, err := json.Marshal(map[string]any{
		"role":     "assistant",
		"content":  []any{map[string]any{"type": "text", "text": report}},
		"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
	})
	if err != nil {
		panic(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"fixture","timestamp":"1970-01-01T00:00:00.000Z","cwd":"fixture"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
	}
	if prompt != "" {
		user := reportUserMessage(prompt)
		lines = append(lines, `{"type":"message_start","message":`+user+`}`, `{"type":"message_end","message":`+user+`}`)
	}
	// runAgentLoop reports newMessages = [...prompts, assistant]; each entry
	// is the same object serialized in the matching message_end event.
	final := string(message)
	if prompt != "" {
		final = reportUserMessage(prompt) + "," + final
	}
	lines = append(lines,
		reportAssistantStartLine,
		`{"type":"message_end","message":`+string(message)+`}`,
		`{"type":"turn_end","message":`+string(message)+`,"toolResults":[]}`,
		`{"type":"agent_end","messages":[`+final+`],"willRetry":false}`,
		`{"type":"agent_settled"}`,
	)
	var stream bytes.Buffer
	for _, line := range lines {
		stream.WriteString(line + "\n")
	}
	return stream.Bytes()
}

func TestParseReportStreamDiagnostic(t *testing.T) {
	const text = "Requirements analysis fixture report"
	valid := reportStreamFixture(text, "")
	mutated := func(old, replacement string) []byte {
		return bytes.Replace(valid, []byte(old), []byte(replacement), 1)
	}
	message := func(block map[string]any) string {
		encoded, err := json.Marshal(map[string]any{
			"role": "assistant", "content": []any{block},
			"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
		})
		if err != nil {
			panic(err)
		}
		return string(encoded)
	}
	blocks := func(content ...map[string]any) string {
		items := make([]any, 0, len(content))
		for _, block := range content {
			items = append(items, block)
		}
		encoded, err := json.Marshal(map[string]any{
			"role": "assistant", "content": items,
			"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
		})
		if err != nil {
			panic(err)
		}
		return string(encoded)
	}
	// both replaces the terminal message in message_end and turn_end so the
	// turn echo stays consistent and only the content shape is under test.
	both := func(encoded string) []byte {
		stream := mutated(`{"type":"message_end","message":`+message(map[string]any{"type": "text", "text": text})+`}`, `{"type":"message_end","message":`+encoded+`}`)
		stream = bytes.Replace(stream, []byte(`{"type":"turn_end","message":`+message(map[string]any{"type": "text", "text": text})+`,"toolResults":[]}`), []byte(`{"type":"turn_end","message":`+encoded+`,"toolResults":[]}`), 1)
		return bytes.Replace(stream, []byte(`{"type":"agent_end","messages":[`+message(map[string]any{"type": "text", "text": text})+`]`), []byte(`{"type":"agent_end","messages":[`+encoded+`]`), 1)
	}
	end := `{"type":"message_end","message":` + message(map[string]any{"type": "text", "text": text}) + `}`
	agentEnd := `{"type":"agent_end","messages":[` + message(map[string]any{"type": "text", "text": text}) + `],"willRetry":false}`
	turnEnd := `{"type":"turn_end","message":` + message(map[string]any{"type": "text", "text": text}) + `,"toolResults":[]}`
	for _, tc := range []struct {
		name          string
		stream        []byte
		ok            bool
		stage, reason string
	}{
		{"success keeps the accepted report stream language", valid, true, "", ""},
		{"optional agent_settled stays optional", bytes.TrimSuffix(valid, []byte(`{"type":"agent_settled"}`+"\n")), true, "", ""},
		{"empty stream", []byte{}, false, reportStageStream, reportReasonEmpty},
		{"unterminated stream", bytes.TrimSuffix(valid, []byte("\n")), false, reportStageStream, reportReasonUnterminated},
		{"invalid utf-8", append([]byte{0xff}, valid...), false, reportStageStream, reportReasonInvalidUTF8},
		{"malformed record", mutated(`{"type":"agent_start"}`, `{"type":"agent_start"}\n`), false, reportStageStream, reportReasonMalformedRecord},
		{"missing event type", mutated(`{"type":"agent_start"}`, `{"id":"fixture"}`), false, reportStageEnvelope, reportReasonMissingType},
		{"unexpected event", mutated(`{"type":"agent_start"}`, `{"type":"other"}`), false, reportStageEnvelope, reportReasonUnexpected},
		{"invalid assistant message", mutated(end, `{"type":"message_end","message":{"role":"user","content":[]}}`), false, reportStageMessage, reportReasonInvalidMessage},
		{"tool call content", mutated(end, `{"type":"message_end","message":`+message(map[string]any{"type": "toolCall", "id": "x", "name": "read", "arguments": map[string]any{}})+`}`), false, reportStageMessage, reportReasonToolCall},
		{"leading thinking block is discarded", both(blocks(map[string]any{"type": "thinking", "thinking": "reasoning", "thinkingSignature": "sig"}, map[string]any{"type": "text", "text": text})), true, "", ""},
		{"thinking block without text", mutated(end, `{"type":"message_end","message":`+message(map[string]any{"type": "thinking", "thinking": "x"})+`}`), false, reportStageMessage, reportReasonInvalidText},
		{"thinking block after text", both(blocks(map[string]any{"type": "text", "text": text}, map[string]any{"type": "thinking", "thinking": "x"})), false, reportStageMessage, reportReasonInvalidMessage},
		{"two thinking blocks", both(blocks(map[string]any{"type": "thinking", "thinking": "a"}, map[string]any{"type": "thinking", "thinking": "b"})), false, reportStageMessage, reportReasonInvalidText},
		{"invalid text content", mutated(end, `{"type":"message_end","message":`+message(map[string]any{"type": "thinking", "text": "x"})+`}`), false, reportStageMessage, reportReasonInvalidText},
		{"turn echo mismatch", mutated(turnEnd, `{"type":"turn_end","message":`+message(map[string]any{"type": "text", "text": "other"})+`,"toolResults":[]}`), false, reportStageMessage, reportReasonTurnMismatch},
		{"tool results present", mutated(`,"toolResults":[]}`, `,"toolResults":[{}]}`), false, reportStageMessage, reportReasonToolResults},
		{"extra agent messages", mutated(agentEnd, `{"type":"agent_end","messages":[`+message(map[string]any{"type": "text", "text": text})+`,{}],"willRetry":false}`), false, reportStageMessage, reportReasonExtraMessages},
		{"empty agent messages", mutated(agentEnd, `{"type":"agent_end","messages":[],"willRetry":false}`), false, reportStageMessage, reportReasonExtraMessages},
		{"agent message differs from terminal", mutated(agentEnd, `{"type":"agent_end","messages":[`+message(map[string]any{"type": "text", "text": "other"})+`],"willRetry":false}`), false, reportStageMessage, reportReasonExtraMessages},
		{"retry flag", mutated(`"willRetry":false`, `"willRetry":true`), false, reportStageMessage, reportReasonRetryFlag},
		{"incomplete stream", bytes.TrimSuffix(valid, []byte(agentEnd+"\n"+`{"type":"agent_settled"}`+"\n")), false, reportStageStream, reportReasonIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, ok, diagnostic := parseReportStream(tc.stream)
			if ok != tc.ok || ok != (report == text) || ok != (diagnostic == nil) {
				t.Fatalf("parseReportStream() = %q, %t, %#v", report, ok, diagnostic)
			}
			if !ok && (diagnostic.Stage != tc.stage || diagnostic.Reason != tc.reason) {
				t.Fatalf("diagnostic = %s/%s, want %s/%s", diagnostic.Stage, diagnostic.Reason, tc.stage, tc.reason)
			}
		})
	}
}

// reportRouteFixture resolves one valid role-default route fixture for the
// report encoder, mirroring the route resolution used by admission bindings.
func reportRouteFixture(t *testing.T, role qarole.RoleID) qaroute.ResolvedRoute {
	t.Helper()
	route, failure := qaroute.Resolve(qaroute.Request{Role: role, Backend: "pi"}, qaroute.Snapshot{})
	if failure.Code != "" {
		t.Fatalf("qaroute.Resolve(%s) failed: %s", role, failure.Code)
	}
	return route
}

// reportResultSection extracts the final "result <len>\n<value>\n" section from
// one encoded report frame and validates the declared length against the value.
func reportResultSection(t *testing.T, frame []byte) string {
	t.Helper()
	if len(frame) == 0 || frame[len(frame)-1] != '\n' {
		t.Fatalf("frame is not newline terminated: %q", frame)
	}
	body := frame[:len(frame)-1]
	header := bytes.LastIndex(body, []byte("\nresult "))
	if header < 0 {
		t.Fatalf("frame has no result section: %q", frame)
	}
	value := body[header+len("\nresult "):]
	endl := bytes.IndexByte(value, '\n')
	if endl < 0 {
		t.Fatalf("result section header is unterminated: %q", frame)
	}
	length, err := strconv.Atoi(string(value[:endl]))
	if err != nil {
		t.Fatalf("result section length %q is not a number: %v", value[:endl], err)
	}
	carried := value[endl+1:]
	if len(carried) != length {
		t.Fatalf("result section declares %d bytes, carries %d", length, len(carried))
	}
	return string(carried)
}

// TestEncodeReportInput pins the role-aware report result contract: every
// resolved role carries its own substantive plain-text instruction in the
// result section, requirements directives stay exclusive to the requirements
// analyst, and unsupported roles fail closed instead of emitting a fallback.
// The expected instructions are synthetic contract pins, not Pi observations.
func TestEncodeReportInput(t *testing.T) {
	const taskText = "fixture report task"
	const requirements = "Return one plain-text requirements analysis report. Quote every supplied requirement verbatim, flag every inconsistency explicitly, and request an explicit resolution for each conflict without inventing policy. Do not echo identity facts and do not call tools."
	requirementsDirectives := []string{
		"Quote every supplied requirement verbatim",
		"flag every inconsistency explicitly",
		"request an explicit resolution for each conflict",
	}
	executionLimits := []string{
		"Do not echo identity facts",
		"do not call tools",
		"do not claim execution you did not perform",
	}
	for _, tc := range []struct {
		name         string
		role         qarole.RoleID
		instruction  string
		requirements bool
	}{
		{"requirements analyst keeps the requirements contract", qarole.RequirementsAnalyst, requirements, true},
		{"test designer contracts conditions and coverage rationale", qarole.TestDesigner, "Return one plain-text test design report. Propose test conditions and coverage rationale within the supplied scope without inventing requirements. Do not echo identity facts, do not call tools, and do not claim execution you did not perform.", false},
		{"exploratory tester separates observations from proposed checks", qarole.ExploratoryTester, "Return one plain-text exploratory assessment report. Assess the supplied behavior evidence, keep recorded observations separate from proposed checks, and never invent observations or claim live investigation. Do not echo identity facts, do not call tools, and do not claim execution you did not perform.", false},
		{"adversarial tester separates hypotheses from observed findings", qarole.AdversarialTester, "Return one plain-text adversarial review report. Examine assumptions, boundaries, and failure behavior, keeping every hypothesis explicitly distinguished from observed findings without inventing findings. Do not echo identity facts, do not call tools, and do not claim execution you did not perform.", false},
		{"test runner reports undetermined without evidence", qarole.TestRunner, "Return one plain-text test assessment report. Assess the supplied test evidence with explicit attribution and uncertainty; never claim to have run tests, and when evidence is missing report that the outcome cannot be determined rather than inventing pass or fail. Do not echo identity facts, do not call tools, and do not claim execution you did not perform.", false},
		{"evidence auditor forbids fabricated evidence", qarole.EvidenceAuditor, "Return one plain-text evidence audit report. Assess the sufficiency, attribution, and uncertainty of the supplied evidence without fabricating evidence or conclusions. Do not echo identity facts, do not call tools, and do not claim execution you did not perform.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := []byte(taskText)
			frame, err := EncodeReportInput(reportRouteFixture(t, tc.role), strings.Repeat("a", 64), strings.Repeat("b", 64), task)
			if err != nil {
				t.Fatal(err)
			}
			if prefix := "/skill:cortex-" + string(tc.role); !bytes.HasPrefix(frame, []byte(prefix+"\n")) {
				t.Fatalf("frame does not address %s: %q", tc.role, frame)
			}
			section := reportResultSection(t, frame)
			if section != tc.instruction {
				t.Fatalf("result section = %q, want %q", section, tc.instruction)
			}
			for _, directive := range requirementsDirectives {
				if strings.Contains(section, directive) != tc.requirements {
					t.Fatalf("requirements directive %q presence = %t, want %t: %q", directive, !tc.requirements, tc.requirements, section)
				}
			}
			if !tc.requirements {
				for _, limit := range executionLimits {
					if !strings.Contains(section, limit) {
						t.Fatalf("result section lacks execution limit %q: %q", limit, section)
					}
				}
			}
			// The selected instruction drives the framing: the whole frame
			// decomposes exactly into prefix, identity, task, and result
			// sections, with the result section sized to the expected contract.
			prefix := len("/skill:cortex-"+string(tc.role)) + 1
			rest := frame[prefix:]
			if !bytes.HasPrefix(rest, []byte("identity ")) {
				t.Fatalf("frame does not open with the identity section: %q", frame)
			}
			headerEnd := bytes.IndexByte(rest, '\n')
			if headerEnd < 0 {
				t.Fatalf("identity section header is unterminated: %q", frame)
			}
			identityLength, err := strconv.Atoi(string(rest[len("identity "):headerEnd]))
			if err != nil {
				t.Fatalf("identity section length is not a number: %v", err)
			}
			identity := rest[headerEnd+1 : headerEnd+1+identityLength]
			// appendSection terminates every section value with one closing
			// newline beyond sectionSize's header accounting, so each section
			// contributes sectionSize + 1 bytes to the frame.
			want := prefix + (sectionSize("identity", identity) + 1) + (sectionSize("task", task) + 1) + (sectionSize("result", []byte(tc.instruction)) + 1)
			if len(frame) != want {
				t.Fatalf("frame length = %d, want %d from prefix + identity + task + result sections", len(frame), want)
			}
		})
	}
	t.Run("unsupported role fails closed", func(t *testing.T) {
		route := reportRouteFixture(t, qarole.RequirementsAnalyst)
		route.Role = "unknown-role"
		frame, err := EncodeReportInput(route, strings.Repeat("a", 64), strings.Repeat("b", 64), []byte(taskText))
		if err == nil || frame != nil {
			t.Fatalf("EncodeReportInput() = %q, %v; want closed rejection", frame, err)
		}
	})
	t.Run("largest valid task stays within the frame bound", func(t *testing.T) {
		task := bytes.Repeat([]byte("x"), qaadmission.MaxTaskBytes)
		frame, err := EncodeReportInput(reportRouteFixture(t, qarole.EvidenceAuditor), strings.Repeat("a", 64), strings.Repeat("b", 64), task)
		if err != nil {
			t.Fatalf("EncodeReportInput() rejected the largest valid task: %v", err)
		}
		if len(frame) > qaadmission.MaxRequestBytes {
			t.Fatalf("frame length %d exceeds the %d byte bound", len(frame), qaadmission.MaxRequestBytes)
		}
	})
}

// TestParseReportStreamPromptEcho pins the runAgentLoop input echo contract:
// the parser accepts one strictly validated initial user prompt echo pair
// before the assistant sequence, then normalizes the same report as before.
func TestParseReportStreamPromptEcho(t *testing.T) {
	const text = "Requirements analysis fixture report"
	const prompt = "identity fixture\ntask fixture"
	valid := reportStreamFixture(text, prompt)
	echoStart := `{"type":"message_start","message":` + reportUserMessage(prompt) + `}`
	echoEnd := `{"type":"message_end","message":` + reportUserMessage(prompt) + `}`
	for _, tc := range []struct {
		name          string
		stream        []byte
		ok            bool
		stage, reason string
	}{
		{"initial user prompt echo precedes the assistant report", valid, true, "", ""},
		{"mismatched echo end content", bytes.Replace(valid, []byte(echoEnd), []byte(`{"type":"message_end","message":`+reportUserMessage("other")+`}`), 1), false, reportStageMessage, reportReasonInvalidMessage},
		{"repeated echo pair", bytes.Replace(valid, []byte(echoStart+"\n"+echoEnd+"\n"), []byte(echoStart+"\n"+echoEnd+"\n"+echoStart+"\n"+echoEnd+"\n"), 1), false, reportStageMessage, reportReasonInvalidMessage},
		{"unpaired echo start", bytes.Replace(valid, []byte(echoEnd+"\n"), []byte(""), 1), false, reportStageEnvelope, reportReasonUnexpected},
		{"late echo after assistant start", bytes.Replace(bytes.Replace(valid, []byte(echoStart+"\n"+echoEnd+"\n"), []byte(""), 1), []byte(reportAssistantStartLine+"\n"), []byte(reportAssistantStartLine+"\n"+echoStart+"\n"+echoEnd+"\n"), 1), false, reportStageEnvelope, reportReasonUnexpected},
		{"other role echo", bytes.Replace(valid, []byte(`"role":"user"`), []byte(`"role":"toolResult"`), 2), false, reportStageMessage, reportReasonInvalidMessage},
		{"extra echo field", bytes.Replace(valid, []byte(`"timestamp":0`), []byte(`"timestamp":0,"injected":true`), 2), false, reportStageMessage, reportReasonInvalidMessage},
		{"agent_end omits the echoed prompt", bytes.Replace(valid, []byte(`"messages":[`+reportUserMessage(prompt)+`,`), []byte(`"messages":[`), 1), false, reportStageMessage, reportReasonExtraMessages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, ok, diagnostic := parseReportStream(tc.stream)
			if ok != tc.ok || ok != (report == text) || ok != (diagnostic == nil) {
				t.Fatalf("parseReportStream() = %q, %t, %#v", report, ok, diagnostic)
			}
			if !ok && (diagnostic.Stage != tc.stage || diagnostic.Reason != tc.reason) {
				t.Fatalf("diagnostic = %s/%s, want %s/%s", diagnostic.Stage, diagnostic.Reason, tc.stage, tc.reason)
			}
		})
	}
}

// TestPreflightBudgetIsNotTheRunBudget pins the separation T6 found missing:
// preflight never spends the caller's execution budget, and the run receives
// the budget the caller granted rather than whatever preflight left behind.
func TestPreflightBudgetIsNotTheRunBudget(t *testing.T) {
	t.Run("preflight does not inherit the caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		preflight, end := preflightContext(ctx)
		deadline, ok := preflight.Deadline()
		if !ok || time.Until(deadline) < preflightBudget/2 {
			t.Fatalf("preflight deadline = %v ok %v, want its own %v bound", deadline, ok, preflightBudget)
		}
		if spent := end(); spent < 0 {
			t.Fatalf("preflight spent = %v", spent)
		}
	})
	t.Run("preflight still stops on caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		preflight, end := preflightContext(ctx)
		defer end()
		cancel()
		select {
		case <-preflight.Done():
		case <-time.After(time.Second):
			t.Fatal("preflight ignored caller cancellation")
		}
	})
	t.Run("the run budget is restored by what preflight spent", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		spent := 300 * time.Millisecond
		run, cancelRun := runBudget(ctx, spent)
		defer cancelRun()
		deadline, ok := run.Deadline()
		if !ok || time.Until(deadline) <= spent {
			t.Fatalf("run deadline = %v ok %v, want more than the %v preflight spent", deadline, ok, spent)
		}
		if run.Err() != nil {
			t.Fatalf("run budget already exhausted: %v", run.Err())
		}
	})
	t.Run("a caller without a deadline keeps its context", func(t *testing.T) {
		ctx := context.Background()
		run, cancelRun := runBudget(ctx, time.Second)
		defer cancelRun()
		if _, ok := run.Deadline(); ok {
			t.Fatal("runBudget invented a deadline for an unbounded caller")
		}
	})
}
