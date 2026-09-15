package qaadmission

import "testing"

func TestReceiptIDPreservesIndependentIdentityLayers(t *testing.T) {
	receipt := testReceipt()
	baseline := ReceiptID(receipt)
	receipt.Route.Observed.Model = "observed-model"
	if ReceiptID(receipt) == baseline {
		t.Fatal("observed model was not independently framed")
	}
	receipt = testReceipt()
	receipt.Route.Resolved.Effort = "high"
	if ReceiptID(receipt) == baseline {
		t.Fatal("resolved effort was not independently framed")
	}
	receipt = testReceipt()
	receipt.Route.Requested.Provider = "other"
	if ReceiptID(receipt) == baseline {
		t.Fatal("requested provider was not independently framed")
	}
	receipt = testReceipt()
	receipt.Route.Observed.Effort.Value = receipt.Route.Resolved.Effort
	if ReceiptID(receipt) == baseline {
		t.Fatal("copied observed effort was not independently framed")
	}
	if testReceipt().Route.Observed.Effort.Availability != EffortUnobservable || testReceipt().Route.Observed.Effort.Value != "" {
		t.Fatal("unobservable effort was inferred")
	}
}
func TestReceiptIDFramesFieldLengths(t *testing.T) {
	left := testReceipt()
	right := testReceipt()
	left.Target.Revision = "ab"
	right.Target.Revision = "a"
	right.Target.Tree = "b" + right.Target.Tree
	if ReceiptID(left) == ReceiptID(right) {
		t.Fatal("ambiguous field values produced one receipt identity")
	}
}
