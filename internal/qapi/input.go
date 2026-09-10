package qapi

import (
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

const (
	InputContract  = "cortex.qa.pi-input.v1"
	skillContract  = "cortex.qa.pi-skill.v1"
	resultContract = "cortex.qa.pi-result.v1"
)

var resultRequirement = []byte("Return exactly one JSON object conforming to " + resultContract + " with matching input_contract, role, actor_contract, actor_sha256, skill_contract, skill_sha256, route, revision, and fingerprint.")

// InputBinding contains the preflight-resolved identities required by one input.
type InputBinding struct {
	ActorContract, ActorSHA256 string
	SkillContract, SkillSHA256 string
	Route                      qaroute.ResolvedRoute
	Revision, Fingerprint      string
}

// EncodeInput constructs one bounded stdin frame without retaining task or frame bytes.
func EncodeInput(binding InputBinding, task []byte) ([]byte, error) {
	if !validInputBinding(binding) || !validTask(task) {
		return nil, errors.New("invalid Pi input")
	}
	identity := inputIdentity(binding)
	size := len("/skill:cortex-") + len(binding.Route.Role) + 1 + sectionSize("identity", identity) + sectionSize("task", task) + sectionSize("result", resultRequirement)
	if size > qaadmission.MaxRequestBytes {
		return nil, errors.New("Pi input exceeds bound")
	}
	frame := make([]byte, 0, size)
	frame = append(frame, "/skill:cortex-"...)
	frame = append(frame, string(binding.Route.Role)...)
	frame = append(frame, '\n')
	frame = appendSection(frame, "identity", identity)
	frame = appendSection(frame, "task", task)
	frame = appendSection(frame, "result", resultRequirement)
	return frame, nil
}

func inputIdentity(binding InputBinding) []byte {
	route := binding.Route
	return []byte(strings.Join([]string{
		"input_contract " + InputContract,
		"role " + string(route.Role),
		"actor_contract " + binding.ActorContract,
		"actor_sha256 " + binding.ActorSHA256,
		"skill_contract " + binding.SkillContract,
		"skill_sha256 " + binding.SkillSHA256,
		"route_policy " + route.PolicyVersion,
		"route_backend " + route.Backend,
		"route_provider " + route.Provider,
		"route_model " + route.Model,
		"route_effort " + route.Effort,
		"route_profile " + route.ProfileID,
		"route_profile_sha256 " + route.ProfileSHA256,
		"route_override_fields " + strings.Join(route.OverrideFields, ","),
		"revision " + binding.Revision,
		"fingerprint " + binding.Fingerprint,
	}, "\n"))
}

func appendSection(frame []byte, name string, value []byte) []byte {
	frame = append(frame, name...)
	frame = append(frame, ' ')
	frame = strconv.AppendInt(frame, int64(len(value)), 10)
	frame = append(frame, '\n')
	frame = append(frame, value...)
	return append(frame, '\n')
}

func sectionSize(name string, value []byte) int {
	return len(name) + 2 + len(strconv.Itoa(len(value))) + len(value)
}

func validInputBinding(binding InputBinding) bool {
	route := binding.Route
	if _, err := qarole.ValidateSquad([]qarole.RoleID{route.Role}); err != nil || binding.ActorContract != qaactor.ActorContractVersion || binding.SkillContract != skillContract || !lowerSHA256(binding.ActorSHA256) || !lowerSHA256(binding.SkillSHA256) || !validRevision(binding.Revision) || !validFingerprint(binding.Fingerprint) {
		return false
	}
	allowed, failure := qaroute.Resolve(qaroute.Request{Role: route.Role, Backend: "pi"}, qaroute.Snapshot{})
	return failure.Code == "" && route.PolicyVersion == allowed.PolicyVersion && route.Role == allowed.Role && route.Backend == allowed.Backend && route.Provider == allowed.Provider && route.Model == allowed.Model && route.Effort == allowed.Effort && validProfile(route) && validOverrideFields(route.OverrideFields)
}

func validProfile(route qaroute.ResolvedRoute) bool {
	if route.ProfileID == "role-default" {
		return route.ProfileSHA256 == ""
	}
	return validProfileID(route.ProfileID) && lowerSHA256(route.ProfileSHA256)
}

func validProfileID(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validOverrideFields(fields []string) bool {
	allowed := []string{"provider", "model", "effort"}
	for index, field := range fields {
		if index >= len(allowed) || field != allowed[index] {
			return false
		}
	}
	return true
}

func lowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validRevision(value string) bool {
	return (len(value) == 40 || len(value) == 64) && lowerHex(value)
}

func validFingerprint(value string) bool {
	return strings.HasPrefix(value, "candidate.") && lowerSHA256(strings.TrimPrefix(value, "candidate."))
}

func lowerHex(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validTask(task []byte) bool {
	if len(task) == 0 || len(task) > qaadmission.MaxTaskBytes || !utf8.Valid(task) {
		return false
	}
	for _, character := range string(task) {
		if character == '\r' || character != '\n' && character != '\t' && unicode.IsControl(character) {
			return false
		}
	}
	for _, line := range strings.Split(string(task), "\n") {
		line = strings.TrimLeft(line, " \t")
		if strings.HasPrefix(line, "/") || reservedAssignment(line) {
			return false
		}
	}
	return true
}

func reservedAssignment(line string) bool {
	end := strings.IndexAny(line, " \t:=")
	if end < 0 {
		return false
	}
	label := line[:end]
	separator := strings.TrimLeft(line[end:], " \t")
	if !strings.HasPrefix(separator, ":") && !strings.HasPrefix(separator, "=") {
		return false
	}
	for _, reserved := range []string{"actor", "skill", "model", "provider", "effort", "profile", "route", "backend", "tool", "tools", "system-prompt", "identity", "result", "authority", "command"} {
		if strings.EqualFold(label, reserved) {
			return true
		}
	}
	return false
}
