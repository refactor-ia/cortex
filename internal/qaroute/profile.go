package qaroute

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/qarole"
)

var profileIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

type profile struct{ routes map[qarole.RoleID]route }

type profiles map[string]profile

func decodeProfiles(data []byte) (profiles, bool) {
	if len(data) > maxProfileBytes || !utf8.Valid(data) {
		return nil, false
	}
	root, ok := object(data, "schemaVersion", "defaultProfile", "profiles")
	if !ok || !number(root["schemaVersion"], 1) {
		return nil, false
	}
	defaultID, ok := text(root["defaultProfile"], 64)
	if !ok || !profileIDPattern.MatchString(defaultID) {
		return nil, false
	}
	members, ok := object(root["profiles"])
	if !ok || len(members) == 0 || len(members) > 64 {
		return nil, false
	}
	decoded := make(profiles, len(members))
	for id, raw := range members {
		if !profileIDPattern.MatchString(id) {
			return nil, false
		}
		entry, ok := object(raw, "routes")
		if !ok {
			return nil, false
		}
		routes, ok := object(entry["routes"])
		if !ok {
			return nil, false
		}
		parsed := profile{routes: make(map[qarole.RoleID]route, len(routes))}
		for role, rawRoute := range routes {
			roleID := qarole.RoleID(role)
			if _, allowed := defaults[roleID]; !allowed {
				return nil, false
			}
			backend, ok := object(rawRoute, "pi")
			if !ok {
				return nil, false
			}
			fields, ok := object(backend["pi"], "provider", "model", "effort")
			if !ok {
				return nil, false
			}
			provider, providerOK := routeText(fields["provider"])
			model, modelOK := routeText(fields["model"])
			effort, effortOK := routeText(fields["effort"])
			if !providerOK || !modelOK || !effortOK {
				return nil, false
			}
			parsed.routes[roleID] = route{provider, model, effort}
		}
		decoded[id] = parsed
	}
	_, exists := decoded[defaultID]
	return decoded, exists
}

func object(data []byte, allowed ...string) (map[string]json.RawMessage, bool) {
	permitted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		permitted[key] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	values := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err = decoder.Token()
		key, isKey := token.(string)
		if err != nil || !isKey || (!permitted[key] && len(allowed) != 0) {
			return nil, false
		}
		if _, duplicate := values[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		values[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	var extra json.RawMessage
	return values, decoder.Decode(&extra) == io.EOF
}

func text(data json.RawMessage, limit int) (string, bool) {
	var value string
	return value, json.Unmarshal(data, &value) == nil && len(value) > 0 && len(value) <= limit
}

func routeText(data json.RawMessage) (string, bool) {
	if data == nil {
		return "", true
	}
	var value string
	return value, json.Unmarshal(data, &value) == nil && len(value) <= 128
}

func number(data json.RawMessage, want int) bool {
	var value int
	return json.Unmarshal(data, &value) == nil && value == want
}
