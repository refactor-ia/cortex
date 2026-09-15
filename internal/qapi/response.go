package qapi

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

const maxResponseJSONDepth = 32

type responseOutcome struct {
	code                        qaadmission.Code
	provider, model, stopReason string
	diagnostic                  *qaadmission.BoundedDiagnostic
}
type responseOutcomeTerminal struct {
	provider, model, stopReason string
	result                      responseResult
	tool                        bool
}
type responseResult struct {
	contract, inputContract, role string
	actorContract, actorSHA256    string
	skillContract, skillSHA256    string
	revision, fingerprint         string
	route                         responseRoute
}
type responseRoute struct {
	policy, backend, provider, model, effort string
	profile, profileSHA256, overrideFields   string
}

func normalizeResponse(facts runFacts, binding InputBinding) responseOutcome {
	expected, valid := expectedResponse(binding)
	if !valid {
		return responseOutcome{code: qaadmission.CodeNormalizationFailed}
	}
	outcome := responseOutcome{}
	if len(facts.stderr) != 0 {
		var err error
		outcome.diagnostic, err = qaadmission.RedactDiagnostic("stderr", facts.stderr, qaadmission.MaxDiagnosticBytes)
		if err != nil {
			outcome.code = qaadmission.CodeNormalizationFailed
			return outcome
		}
	}
	switch {
	case facts.launchFailed:
		outcome.code = qaadmission.CodeLaunchFailed
	case facts.timedOut:
		outcome.code = qaadmission.CodeExecutionTimedOut
	case facts.exitNonZero || facts.waitFailed:
		outcome.code = qaadmission.CodeExecutionFailed
	case facts.stdoutTruncated || facts.stderrTruncated:
		outcome.code = qaadmission.CodeOutputTruncated
	case len(facts.stdout) == 0:
		outcome.code = qaadmission.CodeOutputSilent
	default:
		terminal, ok := parseResponse(facts.stdout)
		if !ok {
			outcome.code = qaadmission.CodeNormalizationFailed
			return outcome
		}
		outcome.provider, outcome.model, outcome.stopReason = terminal.provider, terminal.model, terminal.stopReason
		outcome.code = classifyResponse(terminal, expected)
	}
	return outcome
}

func expectedResponse(binding InputBinding) (responseResult, bool) {
	if !validInputBinding(binding) {
		return responseResult{}, false
	}
	route := binding.Route
	return responseResult{
		contract: resultContract, inputContract: InputContract, role: string(route.Role),
		actorContract: binding.ActorContract, actorSHA256: binding.ActorSHA256,
		skillContract: binding.SkillContract, skillSHA256: binding.SkillSHA256,
		revision: binding.Revision, fingerprint: binding.Fingerprint,
		route: responseRoute{route.PolicyVersion, route.Backend, route.Provider, route.Model, route.Effort, route.ProfileID, route.ProfileSHA256, strings.Join(route.OverrideFields, ",")},
	}, true
}

func classifyResponse(terminal responseOutcomeTerminal, expected responseResult) qaadmission.Code {
	categories := map[qaadmission.Code]bool{}
	if terminal.tool {
		categories[qaadmission.CodePolicyViolation] = true
	} else if terminal.result != expected {
		if terminal.result.route.provider != "nan" {
			categories[qaadmission.CodeNonNaNRoute] = true
		}
		if responseIdentityMismatch(terminal.result, expected) {
			categories[qaadmission.CodeObservedIdentityMismatch] = true
		}
	}
	if terminal.provider != "nan" {
		categories[qaadmission.CodeNonNaNRoute] = true
	}
	if terminal.model != expected.route.model || terminal.provider == "nan" && terminal.provider != expected.route.provider {
		categories[qaadmission.CodeObservedIdentityMismatch] = true
	}
	if len(categories) != 1 {
		if len(categories) == 0 {
			return ""
		}
		return qaadmission.CodeNormalizationFailed
	}
	for code := range categories {
		return code
	}
	return ""
}

func responseIdentityMismatch(actual, expected responseResult) bool {
	if actual.route.provider != expected.route.provider {
		actual.route.provider = expected.route.provider
	}
	return actual != expected
}

func parseResponse(data []byte) (responseOutcomeTerminal, bool) {
	if !utf8.Valid(data) || len(data) == 0 || data[len(data)-1] != '\n' {
		return responseOutcomeTerminal{}, false
	}
	state, session := 0, false
	var terminal responseOutcomeTerminal
	for _, line := range bytes.Split(data[:len(data)-1], []byte("\n")) {
		event, ok := responseObject(line)
		if !ok {
			return responseOutcomeTerminal{}, false
		}
		kind, ok := stringField(event, "type")
		if !ok {
			return responseOutcomeTerminal{}, false
		}
		switch state {
		case 0:
			if kind == "session" && !session && exactFields(event, "type", "version", "id", "timestamp", "cwd") && versionField(event) && stringFieldOK(event, "id") && stringFieldOK(event, "timestamp") && stringFieldOK(event, "cwd") {
				session = true
			} else if kind == "agent_start" && exactFields(event, "type") {
				state = 1
			} else {
				return responseOutcomeTerminal{}, false
			}
		case 1:
			if kind != "turn_start" || !exactFields(event, "type") {
				return responseOutcomeTerminal{}, false
			}
			state = 2
		case 2:
			if kind != "message_start" || !exactFields(event, "type", "message") || !validAssistant(event["message"], false) {
				return responseOutcomeTerminal{}, false
			}
			state = 3
		case 3:
			if kind == "message_update" && validUpdate(event) {
				continue
			}
			if kind != "message_end" || !exactFields(event, "type", "message") {
				return responseOutcomeTerminal{}, false
			}
			terminal, ok = terminalMessage(event["message"])
			if !ok {
				return responseOutcomeTerminal{}, false
			}
			state = 4
		case 4:
			var tools []json.RawMessage
			if kind != "turn_end" || !exactFields(event, "type", "message", "toolResults") || json.Unmarshal(event["toolResults"], &tools) != nil || len(tools) != 0 {
				return responseOutcomeTerminal{}, false
			}
			turn, valid := terminalMessage(event["message"])
			if !valid || turn != terminal {
				return responseOutcomeTerminal{}, false
			}
			state = 5
		case 5:
			var messages []json.RawMessage
			if kind != "agent_end" || !exactFields(event, "type", "messages", "willRetry") || json.Unmarshal(event["messages"], &messages) != nil || !falseField(event, "willRetry") {
				return responseOutcomeTerminal{}, false
			}
			state = 6
		case 6:
			if kind != "agent_settled" || !exactFields(event, "type") {
				return responseOutcomeTerminal{}, false
			}
			state = 7
		default:
			return responseOutcomeTerminal{}, false
		}
	}
	return terminal, state == 6 || state == 7
}

func responseObject(line []byte) (map[string]json.RawMessage, bool) {
	if len(line) == 0 || bytes.Contains(line, []byte("\r")) || !validJSON(line) {
		return nil, false
	}
	var object map[string]json.RawMessage
	return object, json.Unmarshal(line, &object) == nil
}
func validJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if !jsonValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}
func jsonValue(decoder *json.Decoder, depth int) bool {
	if depth > maxResponseJSONDepth {
		return false
	}
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return false
				}
				name, ok := key.(string)
				if !ok || seen[name] || !jsonValue(decoder, depth+1) {
					return false
				}
				seen[name] = true
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for decoder.More() {
				if !jsonValue(decoder, depth+1) {
					return false
				}
			}
			end, err := decoder.Token()
			return err == nil && end == json.Delim(']')
		}
	}
	return true
}

func validUpdate(event map[string]json.RawMessage) bool {
	if !exactFields(event, "type", "usage", "assistantMessageEvent") || !objectField(event, "usage") {
		return false
	}
	var update map[string]json.RawMessage
	if json.Unmarshal(event["assistantMessageEvent"], &update) != nil {
		return false
	}
	kind, ok := stringField(update, "type")
	if !ok {
		return false
	}
	switch kind {
	case "start":
		return exactFields(update, "type")
	case "text_start", "thinking_start":
		return exactFields(update, "type", "contentIndex") && numberField(update, "contentIndex")
	case "text_delta", "thinking_delta":
		return exactFields(update, "type", "contentIndex", "delta") && numberField(update, "contentIndex") && stringFieldOK(update, "delta")
	case "text_end", "thinking_end":
		return exactFields(update, "type", "contentIndex", "content") && numberField(update, "contentIndex") && stringFieldOK(update, "content")
	case "done":
		return exactFields(update, "type", "reason", "message") && stringFieldOK(update, "reason") && validAssistant(update["message"], false)
	}
	return false
}

func terminalMessage(raw json.RawMessage) (responseOutcomeTerminal, bool) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || !allowedFields(message, "role", "content", "api", "provider", "model", "responseModel", "responseId", "providerThinkingLevel", "diagnostics", "usage", "stopReason", "deferred", "errorMessage", "rawStopReason", "endTurn", "timestamp") {
		return responseOutcomeTerminal{}, false
	}
	role, ok := stringField(message, "role")
	if !ok || role != "assistant" || !stringFieldOK(message, "provider") || !stringFieldOK(message, "model") || !stringFieldOK(message, "stopReason") {
		return responseOutcomeTerminal{}, false
	}
	var content []json.RawMessage
	if json.Unmarshal(message["content"], &content) != nil || len(content) != 1 {
		return responseOutcomeTerminal{}, false
	}
	terminal := responseOutcomeTerminal{}
	terminal.provider, _ = stringField(message, "provider")
	terminal.model, _ = stringField(message, "model")
	terminal.stopReason, _ = stringField(message, "stopReason")
	if toolContent(content[0]) {
		terminal.tool = true
		return terminal, true
	}
	text, ok := textContent(content[0])
	if !ok {
		return responseOutcomeTerminal{}, false
	}
	terminal.result, ok = parseResult([]byte(text))
	return terminal, ok
}

func parseResult(data []byte) (responseResult, bool) {
	object, ok := responseObject(data)
	if !ok || !exactFields(object, "contract", "input_contract", "role", "actor_contract", "actor_sha256", "skill_contract", "skill_sha256", "route", "revision", "fingerprint") {
		return responseResult{}, false
	}
	var route map[string]json.RawMessage
	if json.Unmarshal(object["route"], &route) != nil || !exactFields(route, "route_policy", "route_backend", "route_provider", "route_model", "route_effort", "route_profile", "route_profile_sha256", "route_override_fields") {
		return responseResult{}, false
	}
	result := responseResult{}
	var values = []*string{&result.contract, &result.inputContract, &result.role, &result.actorContract, &result.actorSHA256, &result.skillContract, &result.skillSHA256, &result.revision, &result.fingerprint, &result.route.policy, &result.route.backend, &result.route.provider, &result.route.model, &result.route.effort, &result.route.profile, &result.route.profileSHA256, &result.route.overrideFields}
	for index, field := range []string{"contract", "input_contract", "role", "actor_contract", "actor_sha256", "skill_contract", "skill_sha256", "revision", "fingerprint", "route_policy", "route_backend", "route_provider", "route_model", "route_effort", "route_profile", "route_profile_sha256", "route_override_fields"} {
		source := object
		if index >= 9 {
			source = route
		}
		if value, valid := stringField(source, field); !valid {
			return responseResult{}, false
		} else {
			*values[index] = value
		}
	}
	return result, true
}

func validAssistant(raw json.RawMessage, terminal bool) bool {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || !allowedFields(message, "role", "content", "api", "provider", "model", "responseModel", "responseId", "providerThinkingLevel", "diagnostics", "usage", "stopReason", "deferred", "errorMessage", "rawStopReason", "endTurn", "timestamp") {
		return false
	}
	role, ok := stringField(message, "role")
	if !ok || role != "assistant" {
		return false
	}
	var content []json.RawMessage
	if json.Unmarshal(message["content"], &content) != nil {
		return false
	}
	if terminal && (len(content) != 1 || !validText(content[0]) || !stringFieldOK(message, "provider") || !stringFieldOK(message, "model") || !stringFieldOK(message, "stopReason")) {
		return false
	}
	for _, item := range content {
		var block map[string]json.RawMessage
		if json.Unmarshal(item, &block) != nil || !stringFieldOK(block, "type") || !allowedContent(block) {
			return false
		}
	}
	return true
}
func allowedContent(block map[string]json.RawMessage) bool {
	kind, _ := stringField(block, "type")
	return kind == "text" || kind == "thinking"
}
func textContent(raw json.RawMessage) (string, bool) {
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil || !exactFields(block, "type", "text") && !exactFields(block, "type", "text", "textSignature") {
		return "", false
	}
	kind, ok := stringField(block, "type")
	text, textOK := stringField(block, "text")
	return text, ok && kind == "text" && textOK
}
func toolContent(raw json.RawMessage) bool {
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil || !exactFields(block, "type", "id", "name", "arguments") || !objectField(block, "arguments") {
		return false
	}
	kind, ok := stringField(block, "type")
	return ok && kind == "toolCall" && stringFieldOK(block, "id") && stringFieldOK(block, "name")
}
func validText(raw json.RawMessage) bool {
	_, ok := textContent(raw)
	return ok
}
func exactFields(object map[string]json.RawMessage, fields ...string) bool {
	return len(object) == len(fields) && allowedFields(object, fields...)
}
func allowedFields(object map[string]json.RawMessage, fields ...string) bool {
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	for field := range object {
		if !allowed[field] {
			return false
		}
	}
	return true
}
func stringField(object map[string]json.RawMessage, field string) (string, bool) {
	var value string
	return value, json.Unmarshal(object[field], &value) == nil
}
func stringFieldOK(object map[string]json.RawMessage, field string) bool {
	value, ok := stringField(object, field)
	return ok && value != ""
}
func numberField(object map[string]json.RawMessage, field string) bool {
	var value *float64
	return json.Unmarshal(object[field], &value) == nil && value != nil
}
func versionField(object map[string]json.RawMessage) bool {
	var version *int
	return json.Unmarshal(object["version"], &version) == nil && version != nil && *version == 3
}
func objectField(object map[string]json.RawMessage, field string) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(object[field], &value) == nil && value != nil
}
func falseField(object map[string]json.RawMessage, field string) bool {
	var value *bool
	return json.Unmarshal(object[field], &value) == nil && value != nil && !*value
}
