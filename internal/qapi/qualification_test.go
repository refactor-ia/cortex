package qapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

var expectedBindings = ExpectedBindings{
	SkillName:                "cortex-requirements-analyst",
	RenderedSkillSHA256:      "f2467d0067959ac2b80b79ee1769486d9e8500338a33dd3584e48f739a5ccb40",
	ActorArtifactInputSHA256: "a03bdc9e854328c092528fd1c2ae68e12227abc4990cbff893f7f370965a80ce",
	EffectivePromptSHA256:    "08c5d26867a040459ead5516d0b13b90a60088654341f3fddcf5290ba4049f3f",
}

func TestQualify(t *testing.T) {
	positive := readQualificationFixture(t, "sdk-positive.json")
	noSkill := readQualificationFixture(t, "sdk-no-skill.json")

	tests := []struct {
		name     string
		packet   []byte
		expected ExpectedBindings
	}{
		{"observed SDK and parser facts qualify", positive, expectedBindings},
		{"observed extra SDK tool fails closed", readQualificationFixture(t, "sdk-extra-tool.json"), expectedBindings},
		{"observed missing SDK skill fails closed", noSkill, expectedBindings},
		{"synthetic missing parser facts fail closed", bytesReplace(noSkill, `"loadedSkills": []`, `"loadedSkills": [{"name": "cortex-requirements-analyst"}]`), expectedBindings},
		{"wrong runtime fails closed", bytesReplace(positive, `"0.85.1"`, `"0.85.2"`), expectedBindings},
		{"malformed JSON fails closed", []byte(`{"sdk":`), expectedBindings},
		{"trailing JSON fails closed", append(append([]byte{}, positive...), []byte(` {}`)...), expectedBindings},
		{"invalid expected digest fails closed", positive, ExpectedBindings{SkillName: expectedBindings.SkillName, RenderedSkillSHA256: "not-a-digest", ActorArtifactInputSHA256: expectedBindings.ActorArtifactInputSHA256, EffectivePromptSHA256: expectedBindings.EffectivePromptSHA256}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, code := Qualify(tt.packet, tt.expected)
			if tt.name == "observed SDK and parser facts qualify" {
				if code != "" {
					t.Fatalf("Qualify() code = %q, want pass", code)
				}
				if result.RuntimeVersion != RuntimeVersion || result.ProbeContract != ProbeContract {
					t.Fatalf("Qualify() result = %#v", result)
				}
				return
			}
			if code != qaadmission.CodeUnsupportedRuntime {
				t.Fatalf("Qualify() code = %q, want %q", code, qaadmission.CodeUnsupportedRuntime)
			}
		})
	}
}

func readQualificationFixture(t *testing.T, name string) []byte {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Join("testdata", "pi-0.85.1", "qualification", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func bytesReplace(input []byte, old, new string) []byte {
	return []byte(strings.Replace(string(input), old, new, 1))
}
