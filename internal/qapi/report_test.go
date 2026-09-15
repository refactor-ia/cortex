package qapi

import (
	"bytes"
	"encoding/json"
	"testing"
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
