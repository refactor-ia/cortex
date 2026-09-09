package qaadmission

import "testing"

func TestFixedBoundsAndMinimumSize(t *testing.T) {
	bounds := FixedBounds()
	if bounds.RequestBytes != 128*1024 || bounds.TaskBytes != 64*1024 || bounds.ReceiptBytes != 512*1024 || bounds.DiagnosticBytes != 16*1024 {
		t.Fatal("bootstrap bounds changed")
	}
	minimum := MinimumSize()
	if minimum < len(CodeObservedIdentityMismatch) || !BoundsSatisfiable(minimum) || BoundsSatisfiable(minimum-1) {
		t.Fatal("minimum receipt size does not cover all terminal envelopes")
	}
	if PrelaunchCode(minimum-1) != CodeReceiptBoundUnsatisfiable {
		t.Fatal("one byte below minimum did not fail before launch")
	}
}
