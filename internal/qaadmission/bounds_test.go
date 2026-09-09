package qaadmission

import "testing"

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
