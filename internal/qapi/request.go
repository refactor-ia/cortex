package qapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

const AdmissionRequestContract = "cortex.qa.pi-admission-request.v1"

var errInvalidAdmissionRequest = errors.New("invalid admission request")

// AdmissionRequest is the closed, decoded input for one Pi admission attempt.
type AdmissionRequest struct {
	Role             qarole.RoleID
	Backend          string
	CurrentDirectory string
	Revision         string
	Fingerprint      string
	Task             string
	Profile          string
	Override         qaroute.Override
	TimeoutSeconds   int
}

// DecodeAdmissionRequest accepts only the #89 Pi admission request branch.
func DecodeAdmissionRequest(data []byte) (AdmissionRequest, error) {
	if len(data) == 0 || len(data) > qaadmission.MaxRequestBytes || !utf8.Valid(data) || !validJSON(data) {
		return AdmissionRequest{}, errInvalidAdmissionRequest
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || !exactFields(fields, admissionFields(fields)...) {
		return AdmissionRequest{}, errInvalidAdmissionRequest
	}
	if _, exists := fields["optIn"]; exists || !requiredAdmissionFields(fields) {
		return AdmissionRequest{}, errInvalidAdmissionRequest
	}

	version, ok := admissionInt(fields["schemaVersion"])
	contract, contractOK := admissionString(fields["contract"])
	if !ok || version != 1 || !contractOK || contract != AdmissionRequestContract {
		return AdmissionRequest{}, errInvalidAdmissionRequest
	}
	request, ok := decodeAdmissionValues(fields)
	if !ok || !validAdmissionRequest(request) {
		return AdmissionRequest{}, errInvalidAdmissionRequest
	}
	return request, nil
}

func admissionFields(fields map[string]json.RawMessage) []string {
	allowed := []string{"schemaVersion", "contract", "role", "backend", "currentDirectory", "revision", "fingerprint", "task"}
	for _, field := range []string{"profile", "override", "timeoutSeconds"} {
		if _, exists := fields[field]; exists {
			allowed = append(allowed, field)
		}
	}
	return allowed
}

func requiredAdmissionFields(fields map[string]json.RawMessage) bool {
	for _, field := range []string{"schemaVersion", "contract", "role", "backend", "currentDirectory", "revision", "fingerprint", "task"} {
		if _, exists := fields[field]; !exists {
			return false
		}
	}
	return true
}

func decodeAdmissionValues(fields map[string]json.RawMessage) (AdmissionRequest, bool) {
	role, roleOK := admissionString(fields["role"])
	backend, backendOK := admissionString(fields["backend"])
	cwd, cwdOK := admissionString(fields["currentDirectory"])
	revision, revisionOK := admissionString(fields["revision"])
	fingerprint, fingerprintOK := admissionString(fields["fingerprint"])
	task, taskOK := admissionString(fields["task"])
	if !roleOK || !backendOK || !cwdOK || !revisionOK || !fingerprintOK || !taskOK {
		return AdmissionRequest{}, false
	}
	request := AdmissionRequest{
		Role: qarole.RoleID(role), Backend: backend, CurrentDirectory: cwd,
		Revision: revision, Fingerprint: fingerprint, Task: task,
		TimeoutSeconds: qaadmission.DefaultTimeoutSeconds,
	}
	if raw, exists := fields["profile"]; exists {
		var ok bool
		if request.Profile, ok = admissionString(raw); !ok {
			return AdmissionRequest{}, false
		}
	}
	if raw, exists := fields["override"]; exists {
		var ok bool
		if request.Override, ok = decodeAdmissionOverride(raw); !ok {
			return AdmissionRequest{}, false
		}
	}
	if raw, exists := fields["timeoutSeconds"]; exists {
		var ok bool
		if request.TimeoutSeconds, ok = admissionInt(raw); !ok {
			return AdmissionRequest{}, false
		}
	}
	return request, true
}

func decodeAdmissionOverride(raw json.RawMessage) (qaroute.Override, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !exactFields(fields, overrideFields(fields)...) {
		return qaroute.Override{}, false
	}
	override := qaroute.Override{}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"provider", &override.Provider},
		{"model", &override.Model},
		{"effort", &override.Effort},
	} {
		if raw, exists := fields[field.name]; exists {
			value, ok := admissionString(raw)
			if !ok {
				return qaroute.Override{}, false
			}
			*field.value = value
		}
	}
	return override, true
}

func overrideFields(fields map[string]json.RawMessage) []string {
	allowed := make([]string, 0, 3)
	for _, field := range []string{"provider", "model", "effort"} {
		if _, exists := fields[field]; exists {
			allowed = append(allowed, field)
		}
	}
	return allowed
}

func admissionString(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '"' {
		return "", false
	}
	var value string
	return value, json.Unmarshal(raw, &value) == nil
}

func admissionInt(raw json.RawMessage) (int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
		return 0, false
	}
	var value int
	return value, json.Unmarshal(raw, &value) == nil
}

func validAdmissionRequest(request AdmissionRequest) bool {
	if _, err := qarole.ValidateSquad([]qarole.RoleID{request.Role}); err != nil || request.Backend != "pi" || !absolutePaths(request.CurrentDirectory) || !validRevision(request.Revision) || !validFingerprint(request.Fingerprint) || !validTask([]byte(request.Task)) {
		return false
	}
	return (request.Profile == "" || validProfileID(request.Profile)) && request.TimeoutSeconds >= qaadmission.MinimumTimeoutSeconds && request.TimeoutSeconds <= qaadmission.MaximumTimeoutSeconds
}
