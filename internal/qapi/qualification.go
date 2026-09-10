package qapi

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

var allowedTools = []string{"read", "grep", "find", "ls"}

// ExpectedBindings identifies the skill and actor-artifact inputs that a caller
// already bound. It intentionally does not describe an executable invocation.
type ExpectedBindings struct {
	SkillName                string
	RenderedSkillSHA256      string
	ActorArtifactInputSHA256 string
	EffectivePromptSHA256    string
}

// Qualification is bounded evidence that the observed SDK and parser facts
// match the Pi 0.85.1 qualification contract. It is not actor admission.
type Qualification struct {
	ProbeContract  string
	RuntimeVersion string
	Tools          []string
}

type qualificationPacket struct {
	SDK                    sdkFacts               `json:"sdk"`
	CLIParser              parserFacts            `json:"cliParser"`
	DerivedInputProvenance derivedInputProvenance `json:"derivedInputProvenance"`
}

type sdkFacts struct {
	PackageVersion  string           `json:"packageVersion"`
	ActiveToolNames []string         `json:"activeToolNames"`
	ToolDefinitions []toolDefinition `json:"toolDefinitions"`
	LoadedSkills    []loadedSkill    `json:"loadedSkills"`
}

type toolDefinition struct {
	Name       string     `json:"name"`
	SourceInfo sourceInfo `json:"sourceInfo"`
}

type sourceInfo struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Scope  string `json:"scope"`
	Origin string `json:"origin"`
}

type loadedSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type parserFacts struct {
	SkillPaths               []string   `json:"skillPaths"`
	Tools                    string     `json:"tools"`
	AppendSystemPromptSHA256 string     `json:"appendSystemPromptSHA256"`
	UnknownFlags             [][]string `json:"unknownFlags"`
}

type derivedInputProvenance struct {
	RenderedSkillSHA256      string `json:"renderedSkillSHA256"`
	ActorArtifactInputSHA256 string `json:"actorArtifactInputSHA256"`
	EffectivePromptSHA256    string `json:"effectivePromptSHA256"`
	CaptureOperatorSHA256    string `json:"captureOperatorSHA256"`
}

// Qualify accepts only complete, fixture-shaped SDK and parser observations.
// Every failure is unsupported runtime evidence; it never launches Pi or
// establishes provider, actor-loading, or host-behavior claims.
func Qualify(packet []byte, expected ExpectedBindings) (Qualification, qaadmission.Code) {
	if !validExpectedBindings(expected) {
		return Qualification{}, qaadmission.CodeUnsupportedRuntime
	}

	var observed qualificationPacket
	decoder := json.NewDecoder(bytes.NewReader(packet))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&observed) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return Qualification{}, qaadmission.CodeUnsupportedRuntime
	}
	if !validObservation(observed, expected) {
		return Qualification{}, qaadmission.CodeUnsupportedRuntime
	}

	return Qualification{
		ProbeContract:  ProbeContract,
		RuntimeVersion: RuntimeVersion,
		Tools:          append([]string(nil), allowedTools...),
	}, ""
}

func validExpectedBindings(expected ExpectedBindings) bool {
	return expected.SkillName != "" && digestPattern.MatchString(expected.RenderedSkillSHA256) && digestPattern.MatchString(expected.ActorArtifactInputSHA256) && digestPattern.MatchString(expected.EffectivePromptSHA256)
}

func validObservation(observed qualificationPacket, expected ExpectedBindings) bool {
	if observed.SDK.PackageVersion != RuntimeVersion || !sameStrings(observed.SDK.ActiveToolNames, allowedTools) || !validTools(observed.SDK.ToolDefinitions) {
		return false
	}
	if len(observed.SDK.LoadedSkills) != 1 || observed.SDK.LoadedSkills[0].Name != expected.SkillName {
		return false
	}
	if len(observed.CLIParser.SkillPaths) != 1 || observed.CLIParser.SkillPaths[0] == "" || observed.CLIParser.Tools != "read,grep,find,ls" || observed.CLIParser.AppendSystemPromptSHA256 != expected.ActorArtifactInputSHA256 {
		return false
	}
	provenance := observed.DerivedInputProvenance
	return provenance.RenderedSkillSHA256 == expected.RenderedSkillSHA256 && provenance.ActorArtifactInputSHA256 == expected.ActorArtifactInputSHA256 && provenance.EffectivePromptSHA256 == expected.EffectivePromptSHA256 && digestPattern.MatchString(provenance.CaptureOperatorSHA256)
}

func validTools(definitions []toolDefinition) bool {
	if len(definitions) != len(allowedTools) {
		return false
	}
	for index, tool := range allowedTools {
		definition := definitions[index]
		if definition.Name != tool || definition.SourceInfo != (sourceInfo{Path: "<builtin:" + tool + ">", Source: "builtin", Scope: "temporary", Origin: "top-level"}) {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
