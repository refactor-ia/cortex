package modelprofile

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTransformPiTargets(t *testing.T) {
	tests := []struct {
		profile                          Profile
		provider, model, proposal, apply string
	}{
		{ProfileOpenAI, "openai-codex", "gpt-6-astra", "openai-codex/gpt-5.6-sol", "openai-codex/gpt-5.6-terra"},
		{ProfileNAN, "nan", "glm5.3", "nan/glm5.3", "nan/deepseek-v4-flash"},
		{ProfileMixed, "openai-codex", "gpt-6-astra", "openai-codex/gpt-5.6-sol", "nan/deepseek-v4-flash"},
	}
	for _, tt := range tests {
		t.Run(string(tt.profile), func(t *testing.T) {
			primary, specialists, err := TransformPi([]byte(`{"defaultProvider":"old","defaultModel":"old","defaultThinkingLevel":"low","other":1}`), []byte(`{"model_profiles":{"sdd-proposal":{"model":"old","effort":"high"},"sdd-apply":{"model":"old"},"other":{"model":"leave"}}}`), tt.profile)
			if err != nil {
				t.Fatal(err)
			}
			var p map[string]any
			if err := json.Unmarshal(primary, &p); err != nil {
				t.Fatal(err)
			}
			if p["defaultProvider"] != tt.provider || p["defaultModel"] != tt.model || p["defaultThinkingLevel"] != ConversationalLevelLow || p["other"] != float64(1) {
				t.Fatalf("primary = %s", primary)
			}
			var s struct {
				Profiles map[string]struct {
					Model  string `json:"model"`
					Effort string `json:"effort"`
				} `json:"model_profiles"`
			}
			if err := json.Unmarshal(specialists, &s); err != nil {
				t.Fatal(err)
			}
			if s.Profiles["sdd-proposal"].Model != tt.proposal || s.Profiles["sdd-apply"].Model != tt.apply || s.Profiles["sdd-proposal"].Effort != "high" || s.Profiles["other"].Model != "leave" {
				t.Fatalf("specialists = %s", specialists)
			}
		})
	}
}

func TestTransformPiPreservesAndDoesNotFabricate(t *testing.T) {
	primary := []byte(`{"unrelated":{"large":900719925474099312345,"items":[1,2]},"defaultProvider":"nan","defaultModel":"glm5.3","defaultThinkingLevel":"low"}`)
	specialists := []byte(`{"prompt":"keep","model_profiles":{"sdd-proposal":{"model":"nan/glm5.3","effort":"xhigh","nested":{"n":900719925474099312345}},"unknown":{"model":"keep","effort":7}}}`)
	gotPrimary, gotSpecialists, err := TransformPi(primary, specialists, ProfileOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gotPrimary, primary) {
		t.Fatal("primary was not switched")
	}
	if bytes.Contains(gotSpecialists, []byte(`"sdd-apply"`)) {
		t.Fatalf("fabricated missing role: %s", gotSpecialists)
	}
	if !bytes.Contains(gotPrimary, []byte(`900719925474099312345`)) || !bytes.Contains(gotSpecialists, []byte(`900719925474099312345`)) || !bytes.Contains(gotSpecialists, []byte(`"effort":"xhigh"`)) || !bytes.Contains(gotSpecialists, []byte(`"unknown":{"model":"keep","effort":7}`)) {
		t.Fatalf("non-target data changed: primary=%s specialists=%s", gotPrimary, gotSpecialists)
	}
	_, switched, err := TransformPi(gotPrimary, gotSpecialists, ProfileNAN)
	if err != nil || !bytes.Contains(switched, []byte(`"model":"nan/glm5.3"`)) {
		t.Fatalf("switch to nan: %s (%v)", switched, err)
	}
}

func TestTransformPiRejectsInvalidInputAtomically(t *testing.T) {
	validPrimary := []byte(`{"defaultProvider":"x"}`)
	validSpecialists := []byte(`{"model_profiles":{"sdd-proposal":{"model":"x"}}}`)
	for _, tt := range []struct {
		name                 string
		primary, specialists []byte
		profile              Profile
	}{
		{"malformed", []byte(`{`), validSpecialists, ProfileNAN},
		{"root", []byte(`null`), validSpecialists, ProfileNAN},
		{"duplicate nested", []byte(`{"x":{"a":1,"a":2}}`), validSpecialists, ProfileNAN},
		{"model profiles shape", validPrimary, []byte(`{"model_profiles":[]}`), ProfileNAN},
		{"role shape", validPrimary, []byte(`{"model_profiles":{"sdd-proposal":[]}}`), ProfileNAN},
		{"role model shape", validPrimary, []byte(`{"model_profiles":{"sdd-proposal":{"model":1}}}`), ProfileNAN},
		{"unknown profile", validPrimary, validSpecialists, "nope"},
		{"unrepresentable", validPrimary, validSpecialists, ProfileAnthropic},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary, specialists, err := TransformPi(tt.primary, tt.specialists, tt.profile)
			if err == nil || primary != nil || specialists != nil {
				t.Fatalf("got (%s, %s, %v), want no output and error", primary, specialists, err)
			}
		})
	}
}

func TestTransformPiAlreadyTargetIsBytePreserving(t *testing.T) {
	primary := []byte("{\n  \"defaultProvider\": \"nan\", \"defaultModel\": \"glm5.3\", \"defaultThinkingLevel\": \"low\"\n}\n")
	specialists := []byte("{\n  \"model_profiles\": {\"sdd-proposal\": {\"model\": \"nan/glm5.3\", \"effort\": \"high\"}}\n}\n")
	gotPrimary, gotSpecialists, err := TransformPi(primary, specialists, ProfileNAN)
	if err != nil || !bytes.Equal(gotPrimary, primary) || !bytes.Equal(gotSpecialists, specialists) {
		t.Fatalf("got (%q, %q, %v), want exact input", gotPrimary, gotSpecialists, err)
	}
}
