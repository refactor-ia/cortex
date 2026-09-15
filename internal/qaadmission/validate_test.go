package qaadmission

import (
	"encoding/json"
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

func TestValidateAcceptsBoundedTimeoutBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout int
	}{
		{"minimum", MinimumTimeoutSeconds},
		{"default", DefaultTimeoutSeconds},
		{"maximum", MaximumTimeoutSeconds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := testReceipt()
			bounds, ok := BoundsForTimeout(tc.timeout)
			if !ok {
				t.Fatalf("BoundsForTimeout(%d) rejected valid timeout", tc.timeout)
			}
			receipt.Bounds = bounds
			receipt.ReceiptID = ReceiptID(receipt)
			if err := Validate(receipt); err != nil {
				t.Fatalf("Validate() timeout %d: %v", tc.timeout, err)
			}
		})
	}
}

func TestValidateRejectsAlteredFixedBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BoundsFacts)
	}{
		{"request", func(b *BoundsFacts) { b.RequestBytes-- }},
		{"task", func(b *BoundsFacts) { b.TaskBytes-- }},
		{"profile", func(b *BoundsFacts) { b.ProfileBytes-- }},
		{"stdout", func(b *BoundsFacts) { b.StdoutBytes-- }},
		{"stderr", func(b *BoundsFacts) { b.StderrBytes-- }},
		{"receipt", func(b *BoundsFacts) { b.ReceiptBytes-- }},
		{"diagnostic", func(b *BoundsFacts) { b.DiagnosticBytes-- }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := testReceipt()
			tc.mutate(&receipt.Bounds)
			receipt.ReceiptID = ReceiptID(receipt)
			if err := Validate(receipt); err == nil {
				t.Fatal("Validate accepted altered fixed bounds")
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

func TestCanonicalJSONEnforcesEncodedReceiptBound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  int
		escaped bool
		wantErr bool
	}{
		{"ASCII exact MaxReceiptBytes accepted", MaxReceiptBytes, false, false},
		{"ASCII MaxReceiptBytes plus one rejected", MaxReceiptBytes + 1, false, true},
		{"U+2028 escaped exact MaxReceiptBytes accepted", MaxReceiptBytes, true, false},
		{"U+2028 escaped MaxReceiptBytes plus one rejected", MaxReceiptBytes + 1, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := receiptForEncodedSize(t, tc.target, tc.escaped)
			_, err := CanonicalJSON(receipt)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CanonicalJSON() error = %v, want error %t", err, tc.wantErr)
			}
		})
	}
}

func receiptForEncodedSize(t *testing.T, target int, escaped bool) Receipt {
	t.Helper()
	receipt := testReceipt()
	receipt.Code = CodeObservedIdentityMismatch
	receipt.Route.Observed.Model = "arbitrary-observed-model"
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() semantic fixture: %v", err)
	}

	baseline, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("json.Marshal() baseline: %v", err)
	}
	payloadBytes := target - len(baseline)
	if escaped {
		receipt.Route.Observed.Model += "\u2028"
		payloadBytes -= len(`\u2028`)
	}
	if payloadBytes < 0 {
		t.Fatalf("target %d smaller than baseline %d", target, len(baseline))
	}
	receipt.Route.Observed.Model += strings.Repeat("a", payloadBytes)
	receipt.ReceiptID = ReceiptID(receipt)

	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("json.Marshal() sized receipt: %v", err)
	}
	if len(encoded) != target {
		t.Fatalf("json.Marshal() length = %d, want %d", len(encoded), target)
	}
	if escaped && !strings.Contains(string(encoded), `\u2028`) {
		t.Fatalf("json.Marshal() = %s, want escaped U+2028", encoded)
	}
	return receipt
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
