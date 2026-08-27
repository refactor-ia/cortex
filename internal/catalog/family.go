// Package catalog decodes canonical Cortex catalog manifests.
package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// FamilyManifest is a family manifest version 1.
type FamilyManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Router        string   `json:"router"`
	Capabilities  []string `json:"capabilities"`
	Agents        []string `json:"agents"`
}

var approvedFamilyIDs = []string{
	"reasoning", "model-intelligence", "execution", "quality-assurance", "web",
	"mobile", "pcsoft", "services", "personal", "memory-integration", "documentation",
}
var approvedAgentIDs = []string{
	"pcsoft-expert", "test-runner", "security-audit", "frontend-quality", "flutter-quality", "kb-feeder",
}

// ApprovedFamilyIDs returns a copy of the approved family IDs.
func ApprovedFamilyIDs() []string { return append([]string(nil), approvedFamilyIDs...) }

// ApprovedAgentIDs returns a copy of the approved agent IDs.
func ApprovedAgentIDs() []string { return append([]string(nil), approvedAgentIDs...) }

type familyManifestWire struct {
	SchemaVersion *int      `json:"schemaVersion"`
	ID            *string   `json:"id"`
	Router        *string   `json:"router"`
	Capabilities  *[]string `json:"capabilities"`
	Agents        *[]string `json:"agents"`
}

// DecodeFamilyManifest decodes and validates a version 1 family manifest.
func DecodeFamilyManifest(data []byte) (FamilyManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire familyManifestWire
	if err := decoder.Decode(&wire); err != nil {
		return FamilyManifest{}, fmt.Errorf("decode family manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return FamilyManifest{}, fmt.Errorf("decode family manifest: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.ID == nil || wire.Router == nil || wire.Capabilities == nil || wire.Agents == nil {
		return FamilyManifest{}, fmt.Errorf("family manifest: required field is missing")
	}
	manifest := FamilyManifest{*wire.SchemaVersion, *wire.ID, *wire.Router, *wire.Capabilities, *wire.Agents}
	if manifest.SchemaVersion != 1 {
		return FamilyManifest{}, fmt.Errorf("family manifest: schemaVersion must be 1")
	}
	if !contains(approvedFamilyIDs, manifest.ID) {
		return FamilyManifest{}, fmt.Errorf("family manifest: id is not approved")
	}
	if !canonicalPath(manifest.Router, ".md") {
		return FamilyManifest{}, fmt.Errorf("family manifest: router must be a canonical .md path")
	}
	if !validPaths(manifest.Capabilities, ".json") {
		return FamilyManifest{}, fmt.Errorf("family manifest: capabilities must be unique canonical .json paths")
	}
	if !validAgents(manifest.Agents) {
		return FamilyManifest{}, fmt.Errorf("family manifest: agents must be unique approved IDs")
	}
	return manifest, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func canonicalPath(value, extension string) bool {
	if !strings.HasSuffix(value, extension) || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validPaths(values []string, extension string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !canonicalPath(value, extension) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validAgents(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !contains(approvedAgentIDs, value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
