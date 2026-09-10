package modelprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// TransformPi applies profile to Pi's primary settings and specialist config.
// It is pure: invalid inputs and unsupported profiles return no candidate bytes.
func TransformPi(primaryJSON, specialistJSON []byte, profile Profile) ([]byte, []byte, error) {
	primary, err := ResolvePrimary(profile, RuntimePi)
	if err != nil {
		return nil, nil, err
	}
	provider, model, ok := strings.Cut(primary.ModelID, "/")
	if !ok || provider == "" || model == "" {
		return nil, nil, fmt.Errorf("pi primary model ID %q is not provider/model", primary.ModelID)
	}

	settings, err := decodeObject(primaryJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid Pi primary settings: %w", err)
	}
	specialists, err := decodeObject(specialistJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid Pi specialist config: %w", err)
	}

	primaryChanged := setString(settings, "defaultProvider", provider)
	primaryChanged = setString(settings, "defaultModel", model) || primaryChanged
	primaryChanged = setString(settings, "defaultThinkingLevel", primary.ConversationalLevel) || primaryChanged

	specialistChanged, err := transformSpecialists(specialists, profile)
	if err != nil {
		return nil, nil, err
	}
	if !primaryChanged && !specialistChanged {
		return primaryJSON, specialistJSON, nil
	}

	outPrimary, outSpecialists := primaryJSON, specialistJSON
	if primaryChanged {
		outPrimary, err = json.Marshal(settings)
		if err != nil {
			return nil, nil, fmt.Errorf("encode Pi primary settings: %w", err)
		}
	}
	if specialistChanged {
		outSpecialists, err = json.Marshal(specialists)
		if err != nil {
			return nil, nil, fmt.Errorf("encode Pi specialist config: %w", err)
		}
	}
	return outPrimary, outSpecialists, nil
}

func transformSpecialists(config map[string]json.RawMessage, profile Profile) (bool, error) {
	rawProfiles, present := config["model_profiles"]
	if !present {
		return false, nil
	}
	profiles, err := decodeObject(rawProfiles)
	if err != nil {
		return false, fmt.Errorf("invalid model_profiles: %w", err)
	}

	roles, err := SpecialistRoles(profile)
	if err != nil {
		return false, err
	}
	changed := false
	for _, role := range roles {
		rawRole, present := profiles[role]
		if !present {
			continue
		}
		roleConfig, err := decodeObject(rawRole)
		if err != nil {
			return false, fmt.Errorf("invalid model_profiles role %q: %w", role, err)
		}
		rawModel, present := roleConfig["model"]
		if !present {
			return false, fmt.Errorf("invalid model_profiles role %q: missing model", role)
		}
		var current string
		if err := json.Unmarshal(rawModel, &current); err != nil {
			return false, fmt.Errorf("invalid model_profiles role %q model: %w", role, err)
		}
		desired, err := ResolveSpecialist(profile, RuntimePi, role)
		if err != nil {
			return false, err
		}
		if current == desired {
			continue
		}
		roleConfig["model"], _ = json.Marshal(desired)
		profiles[role], err = json.Marshal(roleConfig)
		if err != nil {
			return false, fmt.Errorf("encode model_profiles role %q: %w", role, err)
		}
		changed = true
	}
	if changed {
		config["model_profiles"], err = json.Marshal(profiles)
		if err != nil {
			return false, fmt.Errorf("encode model_profiles: %w", err)
		}
	}
	return changed, nil
}

func setString(object map[string]json.RawMessage, key, want string) bool {
	var got string
	if raw, present := object[key]; present && json.Unmarshal(raw, &got) == nil && got == want {
		return false
	}
	object[key], _ = json.Marshal(want)
	return true
}

func decodeObject(input []byte) (map[string]json.RawMessage, error) {
	if err := validateJSON(input); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("root must be an object")
	}
	return object, nil
}

// validateJSON rejects duplicate object keys at every nesting level before any
// object is decoded into a map, where encoding/json would otherwise overwrite them.
func validateJSON(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := validateValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter, ok := token.(json.Delim); {
	case !ok:
		return nil
	case delimiter == '{':
		keys := map[string]struct{}{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name := key.(string)
			if _, duplicate := keys[name]; duplicate {
				return fmt.Errorf("duplicate key %q", name)
			}
			keys[name] = struct{}{}
			if err := validateValue(decoder); err != nil {
				return err
			}
		}
	case delimiter == '[':
		for decoder.More() {
			if err := validateValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	_, err = decoder.Token()
	return err
}
