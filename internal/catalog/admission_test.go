package catalog

import (
	"strings"
	"testing"
)

func admissionManifest(provenance, license string) []byte {
	urls := ""
	if provenance == ProvenanceThirdParty {
		urls = `,"provenanceUrl":"https://example.com/source","redistributionUrl":"https://example.com/license"`
	}
	return []byte(`{"schemaVersion":1,"id":"test-capability","description":"Professional capability description.","family":"web","source":"capabilities/test.md","activation":"automatic","provenance":"` + provenance + `","license":"` + license + `","redistributionAllowed":true` + urls + `}`)
}

func TestEvaluateAdmissionDecisions(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		policy   AdmissionPolicy
		admitted bool
		reason   AdmissionReason
	}{
		{"cortex-owned ignores empty policy", admissionManifest(ProvenanceCortexOwned, "CC-BY-SA-4.0"), AdmissionPolicy{}, true, AdmissionReasonCortexOwned},
		{"approved third-party license", admissionManifest(ProvenanceThirdParty, "MIT"), AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}}, true, AdmissionReasonThirdPartyLicenseApproved},
		{"rejected third-party license", admissionManifest(ProvenanceThirdParty, "GPL-3.0-only"), AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT"}}, false, AdmissionReasonThirdPartyLicenseRejected},
		{"empty policy rejects third-party", admissionManifest(ProvenanceThirdParty, "MIT"), AdmissionPolicy{}, false, AdmissionReasonThirdPartyLicenseRejected},
		{"license matching is case-sensitive", admissionManifest(ProvenanceThirdParty, "MIT"), AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"mit"}}, false, AdmissionReasonThirdPartyLicenseRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest, decision, err := EvaluateAdmission(tc.data, tc.policy)
			if err != nil {
				t.Fatalf("EvaluateAdmission() error = %v", err)
			}
			if manifest.Provenance == "" || decision.Admitted != tc.admitted || decision.Reason != tc.reason {
				t.Errorf("EvaluateAdmission() = (%+v, %+v), want admitted=%t reason=%q", manifest, decision, tc.admitted, tc.reason)
			}
		})
	}
}

func TestEvaluateAdmissionPolicyOrderAndInputAreStable(t *testing.T) {
	data := admissionManifest(ProvenanceThirdParty, "Apache-2.0")
	first := AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT", "Apache-2.0"}}
	second := AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"Apache-2.0", "MIT"}}
	before := append([]string(nil), first.CompatibleThirdPartyLicenses...)

	_, firstDecision, firstErr := EvaluateAdmission(data, first)
	_, secondDecision, secondErr := EvaluateAdmission(data, second)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("EvaluateAdmission() errors = %v, %v", firstErr, secondErr)
	}
	if firstDecision != secondDecision {
		t.Errorf("policy order changed decision: %+v != %+v", firstDecision, secondDecision)
	}
	if strings.Join(first.CompatibleThirdPartyLicenses, ",") != strings.Join(before, ",") {
		t.Errorf("EvaluateAdmission() mutated policy slice: got %q, want %q", first.CompatibleThirdPartyLicenses, before)
	}
}

func TestEvaluateAdmissionRejectsInvalidPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy AdmissionPolicy
	}{
		{"blank", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{""}}},
		{"whitespace", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{" "}}},
		{"untrimmed", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{" MIT"}}},
		{"duplicate", AdmissionPolicy{CompatibleThirdPartyLicenses: []string{"MIT", "MIT"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, decision, err := EvaluateAdmission(admissionManifest(ProvenanceThirdParty, "MIT"), tc.policy)
			if err == nil {
				t.Fatal("EvaluateAdmission() error = nil")
			}
			if manifest != (CapabilityManifest{}) || decision != (AdmissionDecision{}) {
				t.Errorf("EvaluateAdmission() outputs = (%+v, %+v), want zero values", manifest, decision)
			}
			if strings.Contains(err.Error(), "MIT") {
				t.Errorf("policy error leaked input: %q", err)
			}
		})
	}
}

func TestEvaluateAdmissionRejectsMissingDescription(t *testing.T) {
	data := []byte(strings.Replace(string(admissionManifest(ProvenanceCortexOwned, "CC-BY-SA-4.0")), `,"description":"Professional capability description."`, "", 1))
	manifest, decision, err := EvaluateAdmission(data, AdmissionPolicy{})
	if err == nil || manifest != (CapabilityManifest{}) || decision != (AdmissionDecision{}) {
		t.Fatalf("EvaluateAdmission() = (%+v, %+v, %v), want zero values and error", manifest, decision, err)
	}
}

func TestEvaluateAdmissionInvalidManifestReturnsGenericError(t *testing.T) {
	secret := "private-license-value"
	data := []byte(`{"schemaVersion":1,"license":"` + secret + `"}`)
	manifest, decision, err := EvaluateAdmission(data, AdmissionPolicy{})
	if err == nil {
		t.Fatal("EvaluateAdmission() error = nil")
	}
	if manifest != (CapabilityManifest{}) || decision != (AdmissionDecision{}) {
		t.Errorf("EvaluateAdmission() outputs = (%+v, %+v), want zero values", manifest, decision)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(string(decision.Reason), secret) {
		t.Errorf("EvaluateAdmission() leaked manifest input in error or reason: %q, %q", err, decision.Reason)
	}
}
