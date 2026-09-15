package modelprofile

import (
	"encoding/json"
	"fmt"
)

// TransformOpenCode applies a profile to OpenCode's existing primary agent and
// existing catalog specialist agents. It is pure and does not establish whether
// project, JSONC, or CLI overrides take precedence; I/O preflight owns that.
// A real change is compact-marshaled, while a no-op returns configJSON verbatim.
func TransformOpenCode(configJSON []byte, profile Profile) ([]byte, error) {
	primary, err := ResolvePrimary(profile, RuntimeOpenCode)
	if err != nil {
		return nil, err
	}
	config, err := decodeObject(configJSON)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode config: %w", err)
	}
	if err := requireTargetString(config, "default_agent", "gentle-orchestrator", true); err != nil {
		return nil, err
	}
	if err := requireTargetString(config, "model", primary.ModelID, true); err != nil {
		return nil, err
	}

	rawAgents, ok := config["agent"]
	if !ok {
		return nil, fmt.Errorf("OpenCode agent configuration is required")
	}
	agents, err := decodeObject(rawAgents)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode agent configuration: %w", err)
	}
	rawPrimary, ok := agents["gentle-orchestrator"]
	if !ok {
		return nil, fmt.Errorf("OpenCode agent.gentle-orchestrator is required")
	}
	primaryAgent, err := decodeObject(rawPrimary)
	if err != nil {
		return nil, fmt.Errorf("invalid OpenCode primary agent: %w", err)
	}
	if err := requireTargetString(primaryAgent, "mode", "primary", false); err != nil {
		return nil, fmt.Errorf("invalid OpenCode primary agent: %w", err)
	}
	if err := checkReasoning(primaryAgent, primary.ConversationalLevel); err != nil {
		return nil, fmt.Errorf("invalid OpenCode primary agent: %w", err)
	}
	if err := allowStringOrNull(primaryAgent, "model"); err != nil {
		return nil, fmt.Errorf("invalid OpenCode primary model: %w", err)
	}
	if err := allowStringOrNull(primaryAgent, "variant"); err != nil {
		return nil, fmt.Errorf("invalid OpenCode primary variant: %w", err)
	}

	changed := setString(primaryAgent, "model", primary.ModelID)
	changed = setString(primaryAgent, "variant", primary.ConversationalLevel) || changed
	if changed {
		if agents["gentle-orchestrator"], err = json.Marshal(primaryAgent); err != nil {
			return nil, fmt.Errorf("encode OpenCode primary agent: %w", err)
		}
	}
	roles, err := SpecialistRoles(profile)
	if err != nil {
		return nil, err
	}
	for _, role := range roles {
		rawAgent, present := agents[role]
		if !present {
			continue
		}
		agent, err := decodeObject(rawAgent)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenCode specialist agent %q: %w", role, err)
		}
		desired, err := ResolveSpecialist(profile, RuntimeOpenCode, role)
		if err != nil {
			return nil, err
		}
		if err := allowStringOrNull(agent, "model"); err != nil {
			return nil, fmt.Errorf("invalid OpenCode specialist agent %q model: %w", role, err)
		}
		if !setString(agent, "model", desired) {
			continue
		}
		if agents[role], err = json.Marshal(agent); err != nil {
			return nil, fmt.Errorf("encode OpenCode specialist agent %q: %w", role, err)
		}
		changed = true
	}
	if !changed {
		return configJSON, nil
	}
	config["agent"], err = json.Marshal(agents)
	if err != nil {
		return nil, fmt.Errorf("encode OpenCode agent configuration: %w", err)
	}
	out, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode OpenCode config: %w", err)
	}
	return out, nil
}

// requireTargetString accepts an absent optional key, but an explicit routing
// override must already be the selected target.
func requireTargetString(object map[string]json.RawMessage, key, want string, optional bool) error {
	raw, present := object[key]
	if !present && optional {
		return nil
	}
	var got string
	if !present || json.Unmarshal(raw, &got) != nil || got != want {
		return fmt.Errorf("%q must be %q", key, want)
	}
	return nil
}

// allowStringOrNull permits a missing or null target slot to be filled, but
// never overwrites a malformed non-string value.
func allowStringOrNull(object map[string]json.RawMessage, key string) error {
	raw, present := object[key]
	if !present || string(raw) == "null" {
		return nil
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("%q must be a string or null", key)
	}
	return nil
}

func checkReasoning(agent map[string]json.RawMessage, level string) error {
	if err := requireTargetString(agent, "reasoningEffort", level, true); err != nil {
		return err
	}
	rawOptions, present := agent["options"]
	if !present {
		return nil
	}
	options, err := decodeObject(rawOptions)
	if err != nil {
		return fmt.Errorf("options must be an object: %w", err)
	}
	return requireTargetString(options, "reasoningEffort", level, true)
}
