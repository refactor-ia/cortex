package qaadmission

import (
	"bytes"
	"testing"
)

func TestBoundsForTimeout(t *testing.T) {
	fixed := FixedBounds()
	for _, tc := range []struct {
		name    string
		timeout int
		valid   bool
	}{
		{"minimum", MinimumTimeoutSeconds, true},
		{"default", DefaultTimeoutSeconds, true},
		{"maximum", MaximumTimeoutSeconds, true},
		{"below minimum", MinimumTimeoutSeconds - 1, false},
		{"above maximum", MaximumTimeoutSeconds + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds, ok := BoundsForTimeout(tc.timeout)
			if ok != tc.valid {
				t.Fatalf("BoundsForTimeout(%d) valid = %t, want %t", tc.timeout, ok, tc.valid)
			}
			if !ok {
				return
			}
			if bounds.TimeoutSeconds != tc.timeout {
				t.Fatalf("BoundsForTimeout(%d) timeout = %d", tc.timeout, bounds.TimeoutSeconds)
			}
			bounds.TimeoutSeconds = fixed.TimeoutSeconds
			if bounds != fixed {
				t.Fatalf("BoundsForTimeout(%d) changed fixed caps: %#v", tc.timeout, bounds)
			}
		})
	}
}

func TestBoundsForTimeoutDefaultKeepsCanonicalJSON(t *testing.T) {
	receipt := testReceipt()
	baseline, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatalf("CanonicalJSON() baseline: %v", err)
	}
	bounds, ok := BoundsForTimeout(DefaultTimeoutSeconds)
	if !ok {
		t.Fatal("BoundsForTimeout(default) rejected the default timeout")
	}
	receipt.Bounds = bounds
	receipt.ReceiptID = ReceiptID(receipt)
	got, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatalf("CanonicalJSON() default timeout bounds: %v", err)
	}
	if !bytes.Equal(got, baseline) {
		t.Fatalf("CanonicalJSON() changed for the default timeout\n got: %s\nwant: %s", got, baseline)
	}
}

func TestFixedBoundsAndMinimumSize(t *testing.T) {
	bounds := FixedBounds()
	if bounds.RequestBytes != 128*1024 || bounds.TaskBytes != 64*1024 || bounds.ReceiptBytes != 512*1024 || bounds.DiagnosticBytes != 16*1024 {
		t.Fatal("bootstrap bounds changed")
	}
	receipt := testReceipt()
	receipt.Status, receipt.Code, receipt.AttemptedRun = StatusNonPassing, CodeAdapterUnavailable, false
	receipt.Availability = AvailabilityFacts{}
	receipt.Execution = ExecutionFacts{}
	receipt.Route.Observed = ObservedIdentity{Effort: UnobservableEffort()}
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() truthful prelaunch receipt: %v", err)
	}
	minimum := MinimumSize(receipt)
	if minimum < len(CodeObservedIdentityMismatch) || minimum > receipt.Bounds.ReceiptBytes || !BoundsSatisfiable(receipt, minimum) || BoundsSatisfiable(receipt, minimum-1) {
		t.Fatal("minimum receipt size does not cover all terminal envelopes")
	}
	if PrelaunchCode(receipt, minimum-1) != CodeReceiptBoundUnsatisfiable {
		t.Fatal("one byte below minimum did not fail before launch")
	}
	admitted := receipt
	admitted.Status, admitted.Code, admitted.AttemptedRun = StatusAdmitted, CodeAdmitted, true
	admitted.ReceiptID = ReceiptID(admitted)
	if _, err := CanonicalJSON(admitted); err == nil {
		t.Fatal("CanonicalJSON accepted admitted receipt without evidence")
	}
}
