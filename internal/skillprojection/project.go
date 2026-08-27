// Package skillprojection projects neutral Agent Skills source into runtime-specific memory.
package skillprojection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

const TranslationDisclosure = "Dormant skills add disable-model-invocation: true to preserve explicit-only invocation."

// ReasonCode describes why a runtime projection has no payload.
type ReasonCode string

const dormantEnforcementUnavailable ReasonCode = "dormant_enforcement_unavailable"

// Plan is an immutable in-memory provider projection. It has no install path,
// materialization, touch, transaction authority, or runtime compatibility claim.
type Plan struct {
	assessment projection.Assessment
	reasonCode ReasonCode
	skills     []ProjectedSkill
}

// Assessment returns the runtime and source snapshot-bound projection assessment.
func (plan Plan) Assessment() projection.Assessment { return plan.assessment }

// ReasonCode returns an empty value unless the projection is unrepresentable.
func (plan Plan) ReasonCode() ReasonCode { return plan.reasonCode }

// Skills returns detached non-nil skills in neutral lexicographic order.
func (plan Plan) Skills() []ProjectedSkill {
	skills := make([]ProjectedSkill, len(plan.skills))
	for index, skill := range plan.skills {
		skills[index] = cloneSkill(skill)
	}
	return skills
}

// ProjectedSkill is one runtime-specific, logical skill document.
type ProjectedSkill struct {
	capabilityID string
	logicalID    string
	activation   string
	sha256       string
	content      []byte
}

func (skill ProjectedSkill) CapabilityID() string { return skill.capabilityID }
func (skill ProjectedSkill) LogicalID() string    { return skill.logicalID }
func (skill ProjectedSkill) Activation() string   { return skill.activation }
func (skill ProjectedSkill) SHA256() string       { return skill.sha256 }

// Content returns a defensive, non-nil copy of projected bytes.
func (skill ProjectedSkill) Content() []byte { return append([]byte{}, skill.content...) }

// Build creates a pure runtime-specific projection without inspecting or changing a runtime.
func Build(runtimeID runtimematrix.RuntimeID, sources skillrender.Set) (Plan, error) {
	skills, dormant, err := validateSources(sources)
	if err != nil {
		return Plan{}, invalidInput()
	}
	result, disclosure, reason := projection.Exact, "", ReasonCode("")
	if dormant {
		switch runtimeID {
		case runtimematrix.RuntimePi, runtimematrix.RuntimeClaudeCode:
			result, disclosure = projection.Translated, TranslationDisclosure
			for index := range skills {
				if skills[index].activation == catalog.ActivationDormant {
					if skills[index].content, err = translateDormant(skills[index].content); err != nil {
						return Plan{}, invalidInput()
					}
					skills[index].sha256 = digest(skills[index].content)
				}
			}
		case runtimematrix.RuntimeOpenCode:
			result, reason, skills = projection.Unrepresentable, dormantEnforcementUnavailable, make([]ProjectedSkill, 0)
		}
	}
	assessment, err := projection.NewAssessment(runtimeID, sources.SnapshotFingerprint(), result, disclosure)
	if err != nil {
		return Plan{}, invalidInput()
	}
	return Plan{assessment: assessment, reasonCode: reason, skills: skills}, nil
}

func validateSources(sources skillrender.Set) ([]ProjectedSkill, bool, error) {
	if !validHash(sources.SnapshotFingerprint()) {
		return nil, false, invalidInput()
	}
	rendered, skills, seen := sources.Skills(), make([]ProjectedSkill, 0), map[string]struct{}{}
	previous, dormant := "", false
	for _, source := range rendered {
		capability, logical, content := source.CapabilityID(), source.LogicalID(), source.Content()
		if !validID(capability) || logical != "skills/"+capability || (previous != "" && previous >= logical) || !validActivation(source.Activation()) || len(content) == 0 || !utf8.Valid(content) || source.SHA256() != digest(content) {
			return nil, false, invalidInput()
		}
		if _, exists := seen[capability+"\x00"+logical]; exists {
			return nil, false, invalidInput()
		}
		seen[capability+"\x00"+logical], previous = struct{}{}, logical
		skills = append(skills, ProjectedSkill{capability, logical, source.Activation(), source.SHA256(), content})
		dormant = dormant || source.Activation() == catalog.ActivationDormant
	}
	return skills, dormant, nil
}

func translateDormant(content []byte) ([]byte, error) {
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return nil, invalidInput()
	}
	boundary := bytes.Index(content[4:], []byte("\n---\n"))
	if boundary < 0 {
		return nil, invalidInput()
	}
	boundary += 4
	frontmatter := content[:boundary+5]
	marker := bytes.Index(frontmatter, []byte("metadata:\n"))
	if marker < 0 || (marker > 0 && frontmatter[marker-1] != '\n') {
		return nil, invalidInput()
	}
	projected := make([]byte, 0, len(content)+len("disable-model-invocation: true\n"))
	projected = append(projected, content[:marker]...)
	projected = append(projected, "disable-model-invocation: true\n"...)
	return append(projected, content[marker:]...), nil
}

func cloneSkill(skill ProjectedSkill) ProjectedSkill {
	skill.content = append([]byte{}, skill.content...)
	return skill
}
func digest(content []byte) string {
	value := sha256.Sum256(content)
	return hex.EncodeToString(value[:])
}
func invalidInput() error { return errors.New("skill projection: invalid input") }
func validActivation(value string) bool {
	return value == catalog.ActivationAutomatic || value == catalog.ActivationDormant
}
func validID(value string) bool {
	if len(value) > 57 || value == "" || value[0] == '-' || value[len(value)-1] == '-' || strings.Contains(value, "--") {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
