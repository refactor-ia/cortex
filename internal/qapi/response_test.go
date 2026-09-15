package qapi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestResponseTransport(t *testing.T) {
	// This source-derived fixture is a minimal contract fixture, not an observed Pi capture.
	valid := responseFixture(t)
	for _, tc := range []struct {
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
		{"duplicate end", bytes.Replace(valid, []byte(`{"type":"turn_end"`), []byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"{}"}],"provider":"nan","model":"qwen3.6","stopReason":"stop"}}\n{"type":"turn_end"`), 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeResponse(runFacts{stdout: tc.stream}, inputBinding(qarole.RequirementsAnalyst)); got.code != qaadmission.CodeNormalizationFailed {
				t.Fatalf("normalizeResponse() = %#v", got)
			}
		})
	}
}

func TestResponseTerminalAndProcessPrecedence(t *testing.T) {
	valid, binding := responseFixture(t), inputBinding(qarole.RequirementsAnalyst)
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
			if got := normalizeResponse(tc.facts, binding); got.code != tc.code {
				t.Fatalf("normalizeResponse() = %#v, want %q", got, tc.code)
			}
		})
	}
	withoutOptional := bytes.ReplaceAll(valid, []byte("{\"type\":\"session\",\"version\":3,\"id\":\"source-derived\",\"timestamp\":\"1970-01-01T00:00:00.000Z\",\"cwd\":\"<redacted>\"}\n"), nil)
	withoutOptional = bytes.TrimSuffix(withoutOptional, []byte("{\"type\":\"agent_settled\"}\n"))
	if got := normalizeResponse(runFacts{stdout: withoutOptional}, binding); got.code != "" {
		t.Fatalf("optional transport events = %#v", got)
	}
	got := normalizeResponse(runFacts{stdout: valid, stderr: []byte("Authorization: Bearer abcdefgh")}, binding)
	if got.code != "" || got.provider != "nan" || got.model != "qwen3.6" || got.stopReason != "stop" || got.diagnostic == nil || got.diagnostic.Redaction != "authorization-bearer" {
		t.Fatalf("terminal outcome = %#v", got)
	}
	mismatch := bytes.Replace(valid, []byte(`"model":"qwen3.6","stopReason":"stop"},"toolResults"`), []byte(`"model":"other","stopReason":"stop"},"toolResults"`), 1)
	if got := normalizeResponse(runFacts{stdout: mismatch}, binding); got.code != qaadmission.CodeNormalizationFailed {
		t.Fatalf("turn mismatch = %#v", got)
	}
}

func TestResultEcho(t *testing.T) {
	binding, valid := inputBinding(qarole.RequirementsAnalyst), responseFixture(t)
	for _, tc := range []struct {
		name string
		edit func(string) string
	}{
		{"missing", func(text string) string {
			return strings.Replace(text, `,"fingerprint":"candidate.`, `,"missing":"candidate.`, 1)
		}},
		{"unknown", func(text string) string { return strings.Replace(text, `}`, `,"extra":"x"}`, 1) }},
		{"wrong type", func(text string) string { return strings.Replace(text, `"role":"requirements-analyst"`, `"role":1`, 1) }},
		{"duplicate", func(text string) string {
			return strings.Replace(text, `"contract":"cortex.qa.pi-result.v1",`, `"contract":"cortex.qa.pi-result.v1","contract":"cortex.qa.pi-result.v1",`, 1)
		}},
		{"trailing", func(text string) string { return text + "x" }},
		{"alternate route", func(text string) string { return strings.Replace(text, `"route":{`, `"route":{"digest":"x",`, 1) }},
		{"payload", func(text string) string { return strings.Replace(text, `}`, `,"payload":"x"}`, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeResponse(runFacts{stdout: replaceResult(t, valid, binding, tc.edit)}, binding); got.code != qaadmission.CodeNormalizationFailed {
				t.Fatalf("normalizeResponse() = %#v", got)
			}
		})
	}
	for _, field := range []string{"contract", "input_contract", "role", "actor_contract", "actor_sha256", "skill_contract", "skill_sha256", "revision", "fingerprint", "route_policy", "route_backend", "route_provider", "route_model", "route_effort", "route_profile", "route_profile_sha256", "route_override_fields"} {
		for _, mutation := range []string{"missing", "wrong type"} {
			t.Run(field+" "+mutation, func(t *testing.T) {
				stream := mutateResult(t, valid, binding, field, mutation)
				if got := normalizeResponse(runFacts{stdout: stream}, binding); got.code != qaadmission.CodeNormalizationFailed {
					t.Fatalf("normalizeResponse() = %#v", got)
				}
			})
		}
	}
	if got := normalizeResponse(runFacts{stdout: replaceResult(t, valid, binding, func(text string) string {
		return strings.Replace(text, `"role":"requirements-analyst"`, `"role":"test-runner"`, 1)
	})}, binding); got.code != qaadmission.CodeObservedIdentityMismatch {
		t.Fatalf("echo mismatch = %#v", got)
	}
	invalid := binding
	invalid.Route.Model = "glm5.3"
	if got := normalizeResponse(runFacts{stdout: valid}, invalid); got.code != qaadmission.CodeNormalizationFailed {
		t.Fatalf("invalid expected binding = %#v", got)
	}
}

func TestResponseSemanticClassification(t *testing.T) {
	binding, valid := inputBinding(qarole.RequirementsAnalyst), responseFixture(t)
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
		code qaadmission.Code
	}{
		{"non NaN", func(data []byte) []byte {
			return bytes.ReplaceAll(data, []byte(`"provider":"nan"`), []byte(`"provider":"other"`))
		}, qaadmission.CodeNonNaNRoute},
		{"terminal model", func(data []byte) []byte {
			return bytes.ReplaceAll(data, []byte(`"model":"qwen3.6"`), []byte(`"model":"other"`))
		}, qaadmission.CodeObservedIdentityMismatch},
		{"tool content", responseToolContent, qaadmission.CodePolicyViolation},
		{"multiple categories", func(data []byte) []byte {
			data = bytes.ReplaceAll(data, []byte(`"provider":"nan"`), []byte(`"provider":"other"`))
			return bytes.ReplaceAll(data, []byte(`"model":"qwen3.6"`), []byte(`"model":"other"`))
		}, qaadmission.CodeNormalizationFailed},
		{"non NaN and distinct echo", func(data []byte) []byte {
			return replaceResult(t, data, binding, func(text string) string {
				text = strings.Replace(text, `"route_provider":"nan"`, `"route_provider":"other"`, 1)
				return strings.Replace(text, `"role":"requirements-analyst"`, `"role":"test-runner"`, 1)
			})
		}, qaadmission.CodeNormalizationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeResponse(runFacts{stdout: tc.edit(valid)}, binding); got.code != tc.code {
				t.Fatalf("normalizeResponse() = %#v, want %q", got, tc.code)
			}
		})
	}
}

type resultEcho struct {
	Contract      string    `json:"contract"`
	InputContract string    `json:"input_contract"`
	Role          string    `json:"role"`
	ActorContract string    `json:"actor_contract"`
	ActorSHA256   string    `json:"actor_sha256"`
	SkillContract string    `json:"skill_contract"`
	SkillSHA256   string    `json:"skill_sha256"`
	Route         routeEcho `json:"route"`
	Revision      string    `json:"revision"`
	Fingerprint   string    `json:"fingerprint"`
}
type routeEcho struct {
	Policy         string `json:"route_policy"`
	Backend        string `json:"route_backend"`
	Provider       string `json:"route_provider"`
	Model          string `json:"route_model"`
	Effort         string `json:"route_effort"`
	Profile        string `json:"route_profile"`
	ProfileSHA256  string `json:"route_profile_sha256"`
	OverrideFields string `json:"route_override_fields"`
}

func replaceResult(t *testing.T, data []byte, binding InputBinding, edit func(string) string) []byte {
	t.Helper()
	original, err := json.Marshal(resultFor(binding))
	if err != nil {
		t.Fatal(err)
	}
	oldText, err := json.Marshal(string(original))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := json.Marshal(edit(string(original)))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, oldText, replacement)
}
func resultFor(binding InputBinding) resultEcho {
	route := binding.Route
	return resultEcho{resultContract, InputContract, string(route.Role), binding.ActorContract, binding.ActorSHA256, binding.SkillContract, binding.SkillSHA256, routeEcho{route.PolicyVersion, route.Backend, route.Provider, route.Model, route.Effort, route.ProfileID, route.ProfileSHA256, strings.Join(route.OverrideFields, ",")}, binding.Revision, binding.Fingerprint}
}
func mutateResult(t *testing.T, data []byte, binding InputBinding, field, mutation string) []byte {
	t.Helper()
	original, _ := json.Marshal(resultFor(binding))
	var result map[string]any
	if json.Unmarshal(original, &result) != nil {
		t.Fatal("cannot construct result")
	}
	target := result
	if strings.HasPrefix(field, "route_") {
		target = result["route"].(map[string]any)
	}
	if mutation == "missing" {
		delete(target, field)
	} else {
		target[field] = 1
	}
	changed, _ := json.Marshal(result)
	oldText, _ := json.Marshal(string(original))
	newText, _ := json.Marshal(string(changed))
	return bytes.ReplaceAll(data, oldText, newText)
}
func responseToolContent(data []byte) []byte {
	lines := bytes.SplitAfter(data, []byte("\n"))
	for index, line := range lines {
		var event map[string]json.RawMessage
		if json.Unmarshal(line, &event) != nil || (string(event["type"]) != `"message_end"` && string(event["type"]) != `"turn_end"`) {
			continue
		}
		var message map[string]json.RawMessage
		if json.Unmarshal(event["message"], &message) != nil {
			return nil
		}
		message["content"] = json.RawMessage(`[{"type":"toolCall","id":"x","name":"read","arguments":{}}]`)
		event["message"], _ = json.Marshal(message)
		lines[index], _ = json.Marshal(event)
		lines[index] = append(lines[index], '\n')
	}
	return bytes.Join(lines, nil)
}
func responseFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pi-0.85.1", "responses", "source-derived-minimal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
