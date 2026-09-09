package qaadmission

import (
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
	"testing"
)

func testReceipt() Receipt {
	return Receipt{
		Contract: Contract, Status: StatusNonPassing, Code: CodeExecutionFailed, Role: qarole.TestRunner, Backend: "pi",
		Versions:     Versions{Receipt: Contract, Policy: qaroute.PolicyVersion, Profile: qaroute.ProfileContract, Adapter: "adapter", ActorContract: "actor", SkillContract: "skill", InputContract: "input", ProbeContract: "probe", Runtime: "pi-0.84.4"},
		Installation: InstallationIdentity{ID: installstate.InstallationID("0123456789abcdef0123456789abcdef"), CatalogSHA256: "catalog", ActorSourceSHA256: "source", ActorGeneratedSHA256: "generated", ActorBindingSHA256: "binding", SkillGeneratedSHA256: "skill"},
		Target:       TargetIdentity{CWDIdentity: "cwd.hash", Revision: "revision", Tree: "tree", Fingerprint: "fingerprint"},
		Route:        RouteIdentity{Requested: RequestedIdentity{Provider: "nan", Model: "qwen3.6", Effort: "low"}, Resolved: qaroute.ResolvedRoute{PolicyVersion: qaroute.PolicyVersion, Role: qarole.TestRunner, Backend: "pi", Provider: "nan", Model: "qwen3.6", Effort: "low"}, Observed: ObservedIdentity{Provider: "nan", Model: "qwen3.6", Effort: UnobservableEffort()}},
		Bounds:       FixedBounds(), Diagnostic: &BoundedDiagnostic{RetainedSHA256: "diagnostic"},
	}
}
func TestTerminalCodesAreClosed(t *testing.T) {
	if len(TerminalCodes()) != 27 {
		t.Fatalf("terminal codes = %d, want 27", len(TerminalCodes()))
	}
	if !IsTerminalCode(CodeAdmitted) || !IsTerminalCode(CodeReceiptBoundUnsatisfiable) || IsTerminalCode("unknown") {
		t.Fatal("terminal code set is not closed")
	}
	if StatusAdmitted != "admitted" || StatusNonPassing != "non-passing" {
		t.Fatal("receipt statuses changed")
	}
}
func TestReceiptIDChangesWithMandatoryFields(t *testing.T) {
	receipt := testReceipt()
	baseline := ReceiptID(receipt)
	receipt.Code = CodeOutputSilent
	if ReceiptID(receipt) == baseline {
		t.Fatal("terminal code did not change receipt identity")
	}
	receipt = testReceipt()
	receipt.Diagnostic.RetainedSHA256 = "other"
	if ReceiptID(receipt) == baseline {
		t.Fatal("diagnostic metadata did not change receipt identity")
	}
}
