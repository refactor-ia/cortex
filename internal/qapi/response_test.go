package qapi

import (
	"bytes"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"os"
	"path/filepath"
	"testing"
)

func TestResponseTransport(t *testing.T) {
	// This source-derived fixture is a minimal contract fixture, not an observed Pi capture.
	valid := responseFixture(t)
	cases := []struct {
		name   string
		stream []byte
	}{
		{"missing LF", bytes.TrimSuffix(valid, []byte("\n"))},
		{"blank record", bytes.Replace(valid, []byte(`{"type":"agent_start"}`), []byte(`{"type":"agent_start"}\n`), 1)},
		{"CR record", bytes.Replace(valid, []byte(`{"type":"agent_start"}`), []byte("{\"type\":\"agent_start\"}\r"), 1)},
		{"invalid UTF-8", append([]byte{0xff}, valid...)},
		{"control character", append([]byte{'\x01'}, valid...)},
		{"duplicate key", bytes.Replace(valid, []byte(`{"type":"agent_start"}`), []byte(`{"type":"agent_start","type":"agent_start"}`), 1)},
		{"unknown event", bytes.Replace(valid, []byte(`{"type":"agent_start"}`), []byte(`{"type":"other"}`), 1)},
		{"reordered event", bytes.Replace(valid, []byte(`{"type":"turn_start"}`), []byte(`{"type":"message_start","message":{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"pending"}}`), 1)},
		{"tool update", bytes.Replace(valid, []byte(`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"x"}}`), []byte(`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"toolcall_start","contentIndex":0,"id":"x","toolName":"read"}}`), 1)},
		{"tool content", bytes.ReplaceAll(valid, []byte(`{"type":"text","text":"{}"}`), []byte(`{"type":"toolCall","id":"x","name":"read","arguments":{}}`))},
		{"duplicate end", bytes.Replace(valid, []byte(`{"type":"turn_end"`), []byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{}"}],"provider":"nan","model":"qwen3.6","stopReason":"stop"}}\n{"type":"turn_end"`), 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeResponse(runFacts{stdout: tc.stream}); got.code != qaadmission.CodeNormalizationFailed {
				t.Fatalf("normalizeResponse() = %#v", got)
			}
		})
	}
}
func TestResponseTerminalAndProcessPrecedence(t *testing.T) {
	valid := responseFixture(t)
	for _, tc := range []struct {
		name  string
		facts runFacts
		code  qaadmission.Code
	}{
		{"launch failure", runFacts{launchFailed: true, stdout: valid}, qaadmission.CodeLaunchFailed},
		{"timeout", runFacts{timedOut: true, stdout: valid}, qaadmission.CodeExecutionTimedOut},
		{"nonzero", runFacts{exitNonZero: true, stdout: valid}, qaadmission.CodeExecutionFailed},
		{"wait failure", runFacts{waitFailed: true, stdout: valid}, qaadmission.CodeExecutionFailed},
		{"truncated", runFacts{stdoutTruncated: true, stdout: valid}, qaadmission.CodeOutputTruncated},
		{"silent", runFacts{}, qaadmission.CodeOutputSilent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeResponse(tc.facts); got.code != tc.code {
				t.Fatalf("normalizeResponse() = %#v, want %q", got, tc.code)
			}
		})
	}
	withoutOptional := bytes.ReplaceAll(valid, []byte("{\"type\":\"session\",\"version\":3,\"id\":\"source-derived\",\"timestamp\":\"1970-01-01T00:00:00.000Z\",\"cwd\":\"<redacted>\"}\n"), nil)
	withoutOptional = bytes.TrimSuffix(withoutOptional, []byte("{\"type\":\"agent_settled\"}\n"))
	if got := normalizeResponse(runFacts{stdout: withoutOptional}); got.code != "" {
		t.Fatalf("optional transport events = %#v", got)
	}
	got := normalizeResponse(runFacts{stdout: valid, stderr: []byte("Authorization: Bearer abcdefgh")})
	if got.code != "" || got.provider != "nan" || got.model != "qwen3.6" || got.stopReason != "stop" || got.diagnostic == nil || got.diagnostic.Redaction != "authorization-bearer" {
		t.Fatalf("terminal outcome = %#v", got)
	}
	mismatch := bytes.Replace(valid, []byte(`"model":"qwen3.6","stopReason":"stop"},"toolResults"`), []byte(`"model":"other","stopReason":"stop"},"toolResults"`), 1)
	if got := normalizeResponse(runFacts{stdout: mismatch}); got.code != qaadmission.CodeNormalizationFailed {
		t.Fatalf("turn mismatch = %#v", got)
	}
}
func responseFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pi-0.85.1", "responses", "source-derived-minimal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
