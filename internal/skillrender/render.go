// Package skillrender renders catalog capabilities as runtime-neutral Agent Skills source.
package skillrender

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/refactor-ia/cortex/internal/catalog"
)

// Set is canonical Agent Skills source input, not materialization-ready output.
// Runtime providers must translate dormant enforcement or declare it unrepresentable.
type Set struct {
	snapshotFingerprint string
	skills              []RenderedSkill
}

// SnapshotFingerprint returns the source catalog snapshot identity.
func (set Set) SnapshotFingerprint() string { return set.snapshotFingerprint }

// Skills returns a detached, non-nil copy in lexicographic logical-ID order.
func (set Set) Skills() []RenderedSkill {
	skills := make([]RenderedSkill, len(set.skills))
	copy(skills, set.skills)
	return skills
}

// RenderedSkill is one canonical Agent Skills source document.
type RenderedSkill struct {
	capabilityID string
	logicalID    string
	activation   string
	sha256       string
	content      []byte
}

// CapabilityID returns the catalog capability ID.
func (skill RenderedSkill) CapabilityID() string { return skill.capabilityID }

// LogicalID returns the runtime-neutral skill identifier.
func (skill RenderedSkill) LogicalID() string { return skill.logicalID }

// Activation returns the catalog activation metadata without enforcing it.
func (skill RenderedSkill) Activation() string { return skill.activation }

// SHA256 returns the lowercase content SHA-256.
func (skill RenderedSkill) SHA256() string { return skill.sha256 }

// Content returns a defensive, non-nil copy of the canonical source bytes.
func (skill RenderedSkill) Content() []byte {
	content := make([]byte, len(skill.content))
	copy(content, skill.content)
	return content
}

// Render returns the deterministic runtime-neutral Agent Skills source for snapshot.
func Render(snapshot catalog.CatalogSnapshot) (Set, error) {
	if !validHash(snapshot.Fingerprint()) {
		return Set{}, errors.New("skill render: invalid input")
	}
	seen := make(map[string]struct{})
	skills := make([]RenderedSkill, 0)
	for _, family := range snapshot.Families() {
		familyID := family.Manifest().ID
		if !validID(familyID) {
			return Set{}, errors.New("skill render: invalid input")
		}
		for _, capability := range family.Capabilities() {
			manifest, source := capability.Manifest(), capability.Source().Content()
			if manifest.Family != familyID || !validID(manifest.ID) || !validText(manifest.Description) || !validText(manifest.License) || !validActivation(manifest.Activation) || !validProvenance(manifest.Provenance) || len(source) == 0 || !utf8.Valid(source) {
				return Set{}, errors.New("skill render: invalid input")
			}
			logicalID := "skills/" + manifest.ID
			if _, exists := seen[manifest.ID]; exists {
				return Set{}, errors.New("skill render: invalid input")
			}
			seen[manifest.ID] = struct{}{}
			content := render(manifest, source)
			digest := sha256.Sum256(content)
			skills = append(skills, RenderedSkill{manifest.ID, logicalID, manifest.Activation, hex.EncodeToString(digest[:]), content})
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].logicalID < skills[j].logicalID })
	return Set{snapshot.Fingerprint(), skills}, nil
}

func render(manifest catalog.CapabilityManifest, source []byte) []byte {
	quoted := func(value string) string { data, _ := json.Marshal(value); return string(data) }
	header := strings.Join([]string{
		"---",
		"name: " + quoted("cortex-"+manifest.ID),
		"description: " + quoted(manifest.Description),
		"license: " + quoted(manifest.License),
		"metadata:",
		"  cortex-family: " + quoted(manifest.Family),
		"  cortex-activation: " + quoted(manifest.Activation),
		"  cortex-provenance: " + quoted(manifest.Provenance),
		"---",
		"",
	}, "\n")
	return append([]byte(header), source...)
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

func validText(value string) bool {
	return value != "" && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\u2028\u2029")
}

func validActivation(value string) bool {
	return value == catalog.ActivationAutomatic || value == catalog.ActivationDormant
}

func validProvenance(value string) bool {
	return value == catalog.ProvenanceCortexOwned || value == catalog.ProvenanceThirdParty
}
