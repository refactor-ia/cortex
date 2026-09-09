package qaadmission

import (
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

func testReceipt() Receipt {
	hash := strings.Repeat("a", 64)
	receipt := Receipt{
		Contract: Contract, Status: StatusNonPassing, Code: CodeExecutionFailed, AttemptedRun: true, Role: qarole.TestRunner, Backend: "pi",
		Versions:     Versions{Receipt: Contract, Policy: qaroute.PolicyVersion, Profile: qaroute.ProfileContract, Adapter: "cortex.qa.pi-admission.v1", ActorContract: "cortex.qa.pi-actor.v1", SkillContract: "cortex.qa.pi-skill.v1", InputContract: "cortex.qa.pi-input.v1", ProbeContract: "cortex.qa.pi-probe/v1", Runtime: "0.84.4"},
		Installation: InstallationIdentity{ID: installstate.InstallationID("0123456789abcdef0123456789abcdef"), CatalogSHA256: hash, ActorSourceSHA256: hash, ActorGeneratedSHA256: hash, ActorBindingSHA256: hash, SkillGeneratedSHA256: hash},
		Target:       TargetIdentity{CWDIdentity: "cwd." + hash, Revision: strings.Repeat("b", 40), Tree: strings.Repeat("c", 40), Fingerprint: "candidate." + hash},
		Binary:       BinaryIdentity{Contract: BinaryContract, SHA256: hash, SizeBytes: 1},
		Route:        RouteIdentity{Requested: RequestedIdentity{Provider: "nan", Model: "qwen3.6", Effort: "low"}, Resolved: qaroute.ResolvedRoute{PolicyVersion: qaroute.PolicyVersion, Role: qarole.TestRunner, Backend: "pi", Provider: "nan", Model: "qwen3.6", Effort: "low", ProfileID: "role-default", OverrideFields: []string{"provider", "model", "effort"}}, Observed: ObservedIdentity{Provider: "nan", Model: "qwen3.6", Effort: UnobservableEffort()}},
		Availability: AvailabilityFacts{Model: "available", Authentication: "ready", Fallback: "none"},
		Execution:    ExecutionFacts{InvocationContract: "cortex.qa.pi-admission.v1", ToolPolicy: "read,grep,find,ls", RenderedInputSHA256: hash, Stop: "none", Usage: "unavailable", Completeness: "complete", Truncation: "none"},
		Bounds:       FixedBounds(), Diagnostic: &BoundedDiagnostic{Source: "stderr", Redaction: "none", ObservedBytes: "0", RetainedBytes: "0", RetainedSHA256: hash, Truncation: "none", Completeness: "complete"},
	}
	receipt.ReceiptID = ReceiptID(receipt)
	return receipt
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
