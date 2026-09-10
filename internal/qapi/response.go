package qapi

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

const maxResponseJSONDepth = 32

type responseOutcome struct {
	code                        qaadmission.Code
	provider, model, stopReason string
	diagnostic                  *qaadmission.BoundedDiagnostic
}
type responseTerminal struct {
	provider   string
	model      string
	stopReason string
}

func normalizeResponse(facts runFacts) responseOutcome {
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
	}
	return outcome
}
func parseResponse(data []byte) (responseTerminal, bool) {
	if !utf8.Valid(data) || len(data) == 0 || data[len(data)-1] != '\n' {
		return responseTerminal{}, false
	}
	state, session := 0, false
	var terminal responseTerminal
	for _, line := range bytes.Split(data[:len(data)-1], []byte("\n")) {
		event, ok := responseObject(line)
		if !ok {
			return responseTerminal{}, false
		}
		kind, ok := stringField(event, "type")
		if !ok {
			return responseTerminal{}, false
		}
		switch state {
		case 0:
			if kind == "session" && !session && exactFields(event, "type", "version", "id", "timestamp", "cwd") && versionField(event) && stringFieldOK(event, "id") && stringFieldOK(event, "timestamp") && stringFieldOK(event, "cwd") {
				session = true
			} else if kind == "agent_start" && exactFields(event, "type") {
				state = 1
			} else {
				return responseTerminal{}, false
			}
		case 1:
			if kind != "turn_start" || !exactFields(event, "type") {
				return responseTerminal{}, false
			}
			state = 2
		case 2:
			if kind != "message_start" || !exactFields(event, "type", "message") || !validAssistant(event["message"], false) {
				return responseTerminal{}, false
			}
			state = 3
		case 3:
			if kind == "message_update" && validUpdate(event) {
				continue
			}
			if kind != "message_end" || !exactFields(event, "type", "message") {
				return responseTerminal{}, false
			}
			terminal, ok = terminalMessage(event["message"])
			if !ok {
				return responseTerminal{}, false
			}
			state = 4
		case 4:
			if kind != "turn_end" || !exactFields(event, "type", "message", "toolResults") {
				return responseTerminal{}, false
			}
			turn, valid := terminalMessage(event["message"])
			var tools []json.RawMessage
			if !valid || json.Unmarshal(event["toolResults"], &tools) != nil || len(tools) != 0 || turn != terminal {
				return responseTerminal{}, false
			}
			state = 5
		case 5:
			var messages []json.RawMessage
			if kind != "agent_end" || !exactFields(event, "type", "messages", "willRetry") || json.Unmarshal(event["messages"], &messages) != nil || !falseField(event, "willRetry") {
				return responseTerminal{}, false
			}
			state = 6
		case 6:
			if kind != "agent_settled" || !exactFields(event, "type") {
				return responseTerminal{}, false
			}
			state = 7
		default:
			return responseTerminal{}, false
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
func terminalMessage(raw json.RawMessage) (responseTerminal, bool) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || !validAssistant(raw, true) {
		return responseTerminal{}, false
	}
	provider, _ := stringField(message, "provider")
	model, _ := stringField(message, "model")
	stop, _ := stringField(message, "stopReason")
	return responseTerminal{provider, model, stop}, true
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
func validText(raw json.RawMessage) bool {
	var block map[string]json.RawMessage
	if json.Unmarshal(raw, &block) != nil || !allowedFields(block, "type", "text", "textSignature") {
		return false
	}
	kind, ok := stringField(block, "type")
	return ok && kind == "text" && stringFieldOK(block, "text")
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
