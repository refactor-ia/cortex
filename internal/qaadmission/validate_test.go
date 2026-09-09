package qaadmission

import (
	"strings"
	"testing"
)

func TestValidateAcceptsEveryClosedTerminalCode(t *testing.T) {
	for _, code := range TerminalCodes() {
		t.Run(string(code), func(t *testing.T) {
			receipt := testReceipt()
			receipt.Code = code
			receipt.Status = StatusNonPassing
			receipt.AttemptedRun = mustAttempt(code)
			if code == CodeAdmitted {
				receipt.Status = StatusAdmitted
				receipt.AttemptedRun = true
			}
			receipt.ReceiptID = ReceiptID(receipt)
			if _, err := CanonicalJSON(receipt); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateKeepsRequestedResolvedAndObservedFactsSeparate(t *testing.T) {
	receipt := testReceipt()
	receipt.Route.Requested = RequestedIdentity{Provider: "nan"}
	receipt.Route.Resolved.OverrideFields = []string{"provider"}
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() partial requested default: %v", err)
	}

	receipt = testReceipt()
	receipt.Route.Observed.Model = "other-model"
	receipt.Code = CodeObservedIdentityMismatch
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() observed mismatch receipt: %v", err)
	}

	receipt = testReceipt()
	receipt.Route.Requested.Provider = "other"
	receipt.Route.Resolved.OverrideFields = []string{"provider"}
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err == nil {
		t.Fatal("Validate accepted a requested provider outside the resolved route")
	}
}

func TestValidateRequiresCurrentPiRuntime(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime string
		wantErr bool
	}{
		{name: "accepts-current-runtime", runtime: "0.85.1"},
		{name: "rejects-previous-runtime", runtime: "0.84.4", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := testReceipt()
			receipt.Versions.Runtime = tc.runtime
			receipt.ReceiptID = ReceiptID(receipt)
			if err := Validate(receipt); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() runtime %q error = %v, want error %t", tc.runtime, err, tc.wantErr)
			}
		})
	}
}

func TestValidateAllowsTruthfulPrelaunchWithoutExecutionFacts(t *testing.T) {
	receipt := testReceipt()
	receipt.Status = StatusNonPassing
	receipt.Code = CodeAdapterUnavailable
	receipt.AttemptedRun = false
	receipt.Availability = AvailabilityFacts{}
	receipt.Execution = ExecutionFacts{}
	receipt.Route.Observed = ObservedIdentity{Effort: UnobservableEffort()}
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() truthful prelaunch receipt: %v", err)
	}
}

func TestValidateRejectsMalformedTerminalAndTypedFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"unknown-code", func(r *Receipt) { r.Code = "unknown" }},
		{"status-code-pair", func(r *Receipt) { r.Status = StatusAdmitted }},
		{"attempted-prelaunch", func(r *Receipt) { r.Code = CodeAdapterUnavailable; r.AttemptedRun = true }},
		{"receipt-id", func(r *Receipt) { r.ReceiptID = "receipt.bad" }},
		{"installation-id", func(r *Receipt) { r.Installation.ID = "bad" }},
		{"binary-hash", func(r *Receipt) { r.Binary.SHA256 = "bad" }},
		{"binary-size", func(r *Receipt) { r.Binary.SizeBytes = 0 }},
		{"actual-bounds", func(r *Receipt) { r.Bounds.ReceiptBytes-- }},
		{"probe-version", func(r *Receipt) { r.Versions.ProbeContract = "pi-0.84.4" }},
		{"runtime-prefixed-version", func(r *Receipt) { r.Versions.Runtime = "pi-0.85.1" }},
		{"route-policy", func(r *Receipt) { r.Route.Resolved.PolicyVersion = "other" }},
		{"route-role", func(r *Receipt) { r.Route.Resolved.Role = "other" }},
		{"route-profile", func(r *Receipt) {
			r.Route.Resolved.ProfileID = "role-default"
			r.Route.Resolved.ProfileSHA256 = strings.Repeat("a", 64)
		}},
		{"route-overrides", func(r *Receipt) { r.Route.Resolved.OverrideFields = []string{"model", "provider"} }},
		{"inferred-effort", func(r *Receipt) { r.Route.Observed.Effort.Value = r.Route.Resolved.Effort }},
		{"arbitrary-usage", func(r *Receipt) { r.Execution.Usage = "lots" }},
		{"required-truncation", func(r *Receipt) {
			r.Status, r.Code, r.Execution.Completeness, r.Execution.Truncation = StatusAdmitted, CodeAdmitted, "truncated", "1"
		}},
		{"malformed-metadata", func(r *Receipt) { r.Diagnostic.Redaction = "authorization-basic,credential-assignment" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := testReceipt()
			tc.mutate(&receipt)
			if tc.name != "receipt-id" {
				receipt.ReceiptID = ReceiptID(receipt)
			}
			if err := Validate(receipt); err == nil {
				t.Fatal("Validate accepted malformed receipt")
			}
		})
	}
}

func TestCanonicalJSONBindsBinaryWithoutPath(t *testing.T) {
	receipt := testReceipt()
	encoded, err := CanonicalJSON(receipt)
	if err != nil || strings.Contains(string(encoded), "/private/pi") || strings.Contains(string(encoded), "Path") {
		t.Fatalf("invalid binary projection: %v %s", err, encoded)
	}
	baseline := receipt.ReceiptID
	receipt.Binary.SHA256 = strings.Repeat("b", 64)
	if ReceiptID(receipt) == baseline {
		t.Fatal("binary identity was not framed")
	}
}
