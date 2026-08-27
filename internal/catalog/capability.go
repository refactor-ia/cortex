package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	canonicalEvidenceURL = regexp.MustCompile(`^https://[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]+)?(?:[/?#](?:[^\x{0009}-\x{000D}\x{0020}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}%]|%[0-9A-Fa-f]{2})*)?\z`)
	canonicalDescription = regexp.MustCompile(`^[^\x{0009}-\x{000D}\x{0020}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}](?:[^\r\n\x{2028}\x{2029}]*[^\x{0009}-\x{000D}\x{0020}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}])?\z`)
	canonicalLicense     = regexp.MustCompile(`^[^\x{0009}-\x{000D}\x{0020}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}](?:[^\r\n\x{2028}\x{2029}]*[^\x{0009}-\x{000D}\x{0020}\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}])?\z`)
)

const (
	// ActivationAutomatic activates a capability without configuration.
	ActivationAutomatic = "automatic"
	// ActivationDormant requires explicit configuration before activation.
	ActivationDormant = "dormant"
	// ProvenanceCortexOwned identifies repository-authored content.
	ProvenanceCortexOwned = "cortex-owned"
	// ProvenanceThirdParty identifies externally sourced content.
	ProvenanceThirdParty = "third-party"
)

// CapabilityManifest is a capability manifest version 1.
type CapabilityManifest struct {
	SchemaVersion         int    `json:"schemaVersion"`
	ID                    string `json:"id"`
	Description           string `json:"description"`
	Family                string `json:"family"`
	Source                string `json:"source"`
	Activation            string `json:"activation"`
	Provenance            string `json:"provenance"`
	License               string `json:"license"`
	RedistributionAllowed bool   `json:"redistributionAllowed"`
	ProvenanceURL         string `json:"provenanceUrl,omitempty"`
	RedistributionURL     string `json:"redistributionUrl,omitempty"`
}

type capabilityManifestWire struct {
	SchemaVersion         *int    `json:"schemaVersion"`
	ID                    *string `json:"id"`
	Description           *string `json:"description"`
	Family                *string `json:"family"`
	Source                *string `json:"source"`
	Activation            *string `json:"activation"`
	Provenance            *string `json:"provenance"`
	License               *string `json:"license"`
	RedistributionAllowed *bool   `json:"redistributionAllowed"`
	ProvenanceURL         *string `json:"provenanceUrl"`
	RedistributionURL     *string `json:"redistributionUrl"`
}

// DecodeCapabilityManifest decodes and validates a version 1 capability manifest.
func DecodeCapabilityManifest(data []byte) (CapabilityManifest, error) {
	if !utf8.Valid(data) {
		return CapabilityManifest{}, fmt.Errorf("decode capability manifest: invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var wire capabilityManifestWire
	if err := decoder.Decode(&wire); err != nil {
		return CapabilityManifest{}, fmt.Errorf("decode capability manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CapabilityManifest{}, fmt.Errorf("decode capability manifest: trailing JSON")
	}
	if wire.SchemaVersion == nil || wire.ID == nil || wire.Description == nil || wire.Family == nil || wire.Source == nil || wire.Activation == nil || wire.Provenance == nil || wire.License == nil || wire.RedistributionAllowed == nil {
		return CapabilityManifest{}, fmt.Errorf("capability manifest: required field is missing")
	}
	manifest := CapabilityManifest{SchemaVersion: *wire.SchemaVersion, ID: *wire.ID, Description: *wire.Description, Family: *wire.Family, Source: *wire.Source, Activation: *wire.Activation, Provenance: *wire.Provenance, License: *wire.License, RedistributionAllowed: *wire.RedistributionAllowed}
	if wire.ProvenanceURL != nil {
		manifest.ProvenanceURL = *wire.ProvenanceURL
	}
	if wire.RedistributionURL != nil {
		manifest.RedistributionURL = *wire.RedistributionURL
	}
	if manifest.SchemaVersion != 1 {
		return CapabilityManifest{}, fmt.Errorf("capability manifest: schemaVersion must be 1")
	}
	if !canonicalCapabilityID(manifest.ID) || !validDescription(manifest.Description) || !contains(approvedFamilyIDs, manifest.Family) || !canonicalPath(manifest.Source, ".md") || !contains([]string{ActivationAutomatic, ActivationDormant}, manifest.Activation) || !contains([]string{ProvenanceCortexOwned, ProvenanceThirdParty}, manifest.Provenance) || !validLicense(manifest.License) || !manifest.RedistributionAllowed {
		return CapabilityManifest{}, fmt.Errorf("capability manifest: fields are invalid")
	}
	if manifest.Provenance == ProvenanceCortexOwned {
		if manifest.License != "CC-BY-SA-4.0" || wire.ProvenanceURL != nil || wire.RedistributionURL != nil {
			return CapabilityManifest{}, fmt.Errorf("capability manifest: cortex-owned evidence is invalid")
		}
	} else if wire.ProvenanceURL == nil || wire.RedistributionURL == nil || !validHTTPSURL(manifest.ProvenanceURL) || !validHTTPSURL(manifest.RedistributionURL) {
		return CapabilityManifest{}, fmt.Errorf("capability manifest: third-party evidence is invalid")
	}
	return manifest, nil
}

func canonicalCapabilityID(value string) bool {
	if len(value) > 57 || value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return !strings.Contains(value, "--")
}

func validHTTPSURL(value string) bool {
	return canonicalEvidenceURL.MatchString(value)
}

func validDescription(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= 1 && utf8.RuneCountInString(value) <= 1024 && canonicalDescription.MatchString(value)
}

func validLicense(value string) bool {
	return canonicalLicense.MatchString(value)
}
