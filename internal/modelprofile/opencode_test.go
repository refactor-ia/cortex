package modelprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestTransformOpenCodeSwitchesPrimaryAndExistingSpecialists(t *testing.T) {
	input := []byte(`{"default_agent":"gentle-orchestrator","agent":{"gentle-orchestrator":{"mode":"primary","model":"openai/gpt-6-astra","variant":"low","reasoningEffort":"low"},"sdd-proposal":{"model":"old","effort":"high","variant":"keep","reasoningEffort":"keep"},"sdd-apply":{"model":null},"unknown":{"model":"leave"}},"providers":{"x":{"auth":"keep"}},"permissions":{"edit":"ask"},"number":900719925474099312345}`)
	got, err := TransformOpenCode(input, ProfileNAN)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Agent map[string]map[string]json.RawMessage `json:"agent"`
	}
	if err := json.Unmarshal(got, &config); err != nil {
		t.Fatal(err)
	}
	assertString(t, config.Agent["gentle-orchestrator"]["model"], "nan/glm5.3")
	assertString(t, config.Agent["gentle-orchestrator"]["variant"], "low")
	assertString(t, config.Agent["sdd-proposal"]["model"], "nan/glm5.3")
	assertString(t, config.Agent["sdd-apply"]["model"], "nan/deepseek-v4-flash")
	assertString(t, config.Agent["sdd-proposal"]["effort"], "high")
	assertString(t, config.Agent["sdd-proposal"]["variant"], "keep")
	assertString(t, config.Agent["sdd-proposal"]["reasoningEffort"], "keep")
	if _, ok := config.Agent["sdd-design"]; ok || !bytes.Contains(got, []byte(`900719925474099312345`)) || !bytes.Contains(got, []byte(`"unknown":{"model":"leave"}`)) || !bytes.Contains(got, []byte(`"providers":{"x":{"auth":"keep"}}`)) || !bytes.Contains(got, []byte(`"permissions":{"edit":"ask"}`)) {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestTransformOpenCodeOpenAIAndMixed(t *testing.T) {
	for _, profile := range []Profile{ProfileOpenAI, ProfileMixed} {
		t.Run(string(profile), func(t *testing.T) {
			got, err := TransformOpenCode([]byte(`{"agent":{"gentle-orchestrator":{"mode":"primary","model":"nan/glm5.3","variant":"x"},"sdd-proposal":{"model":"nan/glm5.3"},"review-risk":{"model":"nan/glm5.3"}}}`), profile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got, []byte(`"model":"openai/gpt-6-astra"`)) || !bytes.Contains(got, []byte(`"sdd-proposal":{"model":"openai/gpt-5.6-sol"}`)) {
				t.Fatalf("wrong %s output: %s", profile, got)
			}
			if profile == ProfileMixed && !bytes.Contains(got, []byte(`"review-risk":{"model":"nan/glm5.3"}`)) {
				t.Fatalf("mixed mapping: %s", got)
			}
		})
	}
}

func TestTransformOpenCodeRejectsInvalidOrIneffectiveTargetsAtomically(t *testing.T) {
	valid := []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary"},"sdd-proposal":{"model":"old"}}}`)
	for _, tt := range []struct {
		name    string
		input   []byte
		profile Profile
		want    error
	}{
		{"unknown", valid, "unknown", ErrUnknownProfile},
		{"unrepresentable", valid, ProfileAnthropic, ErrUnrepresentable},
		{"missing agent", []byte(`{}`), ProfileNAN, nil},
		{"missing primary", []byte(`{"agent":{}}`), ProfileNAN, nil},
		{"wrong primary mode", []byte(`{"agent":{"gentle-orchestrator":{"mode":"subagent"}}}`), ProfileNAN, nil},
		{"root default override", []byte(`{"default_agent":"other","agent":{"gentle-orchestrator":{"mode":"primary"}}}`), ProfileNAN, nil},
		{"root model conflict", []byte(`{"model":"openai/gpt-6-astra","agent":{"gentle-orchestrator":{"mode":"primary"}}}`), ProfileNAN, nil},
		{"direct reasoning conflict", []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary","reasoningEffort":"high"}}}`), ProfileNAN, nil},
		{"options reasoning conflict", []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary","options":{"reasoningEffort":"high"}}}}`), ProfileNAN, nil},
		{"invalid options", []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary","options":[]}}}`), ProfileNAN, nil},
		{"specialist model type", []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary"},"sdd-proposal":{"model":1}}}`), ProfileNAN, nil},
		{"duplicate", []byte(`{"agent":{"gentle-orchestrator":{"mode":"primary","mode":"primary"}}}`), ProfileNAN, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TransformOpenCode(tt.input, tt.profile)
			if err == nil || got != nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestTransformOpenCodeNoOpPreservesBytesAndAllowsMissingModels(t *testing.T) {
	input := []byte("{\n  \"model\": \"nan/glm5.3\", \"agent\": {\"gentle-orchestrator\": {\"mode\": \"primary\", \"model\": \"nan/glm5.3\", \"variant\": \"low\"}, \"sdd-proposal\": {\"model\": \"nan/glm5.3\"}}\n}\n")
	got, err := TransformOpenCode(input, ProfileNAN)
	if err != nil || !bytes.Equal(got, input) {
		t.Fatalf("got %q, %v", got, err)
	}
	got, err = TransformOpenCode([]byte(`{"agent":{"gentle-orchestrator":{"mode":"primary"},"sdd-proposal":{}}}`), ProfileNAN)
	if err != nil || !bytes.Contains(got, []byte(`"sdd-proposal":{"model":"nan/glm5.3"}`)) {
		t.Fatalf("got %s, %v", got, err)
	}
}

func assertString(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var got string
	if err := json.Unmarshal(raw, &got); err != nil || got != want {
		t.Fatalf("got %s, want %q", raw, want)
	}
}
