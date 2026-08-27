package catalog

import (
	"errors"
	"strings"
)

// AdmissionPolicy is the explicit license policy for third-party capabilities.
type AdmissionPolicy struct {
	CompatibleThirdPartyLicenses []string
}

// AdmissionReason identifies the deterministic basis for an admission decision.
type AdmissionReason string

const (
	// AdmissionReasonCortexOwned admits a valid Cortex-owned capability.
	AdmissionReasonCortexOwned AdmissionReason = "cortex-owned"
	// AdmissionReasonThirdPartyLicenseApproved admits an explicitly approved license.
	AdmissionReasonThirdPartyLicenseApproved AdmissionReason = "third-party-license-approved"
	// AdmissionReasonThirdPartyLicenseRejected rejects an unapproved third-party license.
	AdmissionReasonThirdPartyLicenseRejected AdmissionReason = "third-party-license-rejected"
)

// AdmissionDecision is the result of evaluating a valid capability manifest.
type AdmissionDecision struct {
	Admitted bool
	Reason   AdmissionReason
}

// EvaluateAdmission validates a manifest and evaluates it against an explicit policy.
func EvaluateAdmission(data []byte, policy AdmissionPolicy) (CapabilityManifest, AdmissionDecision, error) {
	approved, err := approvedThirdPartyLicenses(policy)
	if err != nil {
		return CapabilityManifest{}, AdmissionDecision{}, err
	}

	manifest, err := DecodeCapabilityManifest(data)
	if err != nil {
		return CapabilityManifest{}, AdmissionDecision{}, errors.New("capability manifest is invalid")
	}
	if manifest.Provenance == ProvenanceCortexOwned {
		return manifest, AdmissionDecision{Admitted: true, Reason: AdmissionReasonCortexOwned}, nil
	}
	if approved[manifest.License] {
		return manifest, AdmissionDecision{Admitted: true, Reason: AdmissionReasonThirdPartyLicenseApproved}, nil
	}
	return manifest, AdmissionDecision{Reason: AdmissionReasonThirdPartyLicenseRejected}, nil
}

func approvedThirdPartyLicenses(policy AdmissionPolicy) (map[string]bool, error) {
	approved := make(map[string]bool, len(policy.CompatibleThirdPartyLicenses))
	for _, license := range policy.CompatibleThirdPartyLicenses {
		if license == "" || strings.TrimSpace(license) != license || approved[license] {
			return nil, errors.New("admission policy is invalid")
		}
		approved[license] = true
	}
	return approved, nil
}
