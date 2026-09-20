package qaadmission

import (
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaroute"
)

// TestContractsAreDerivedPerBackend proves every recorded contract names the
// executing backend. Before three adapters existed these were Pi-named
// literals; a receipt that still carried them for another backend would read
// as though Pi's contract had been satisfied.
func TestContractsAreDerivedPerBackend(t *testing.T) {
	for _, backend := range []string{"pi", "claude", "opencode"} {
		t.Run(backend, func(t *testing.T) {
			contracts, known := ContractsFor(backend)
			if !known {
				t.Fatalf("ContractsFor(%q) is unknown", backend)
			}
			for name, value := range map[string]string{
				"adapter": contracts.Adapter, "binary": contracts.Binary, "input": contracts.Input,
				"probe": contracts.Probe, "skill": contracts.Skill, "result": contracts.Result,
				"request": contracts.Request,
			} {
				if !strings.HasPrefix(value, "cortex.qa."+backend+"-") {
					t.Fatalf("%s contract %q does not name %q", name, value, backend)
				}
			}
		})
	}
	if _, known := ContractsFor("unadmitted"); known {
		t.Fatal("ContractsFor accepted a backend route policy does not admit")
	}
}

// TestPiContractsAreUnchanged proves the per-backend derivation reproduces the
// exact Pi literals it replaced, byte for byte. A Pi receipt must not change
// because the contracts became per-adapter.
func TestPiContractsAreUnchanged(t *testing.T) {
	contracts, _ := ContractsFor("pi")
	for got, want := range map[string]string{
		contracts.Adapter: "cortex.qa.pi-admission.v1",
		contracts.Binary:  "cortex.qa.pi-binary.v1",
		contracts.Input:   "cortex.qa.pi-input.v1",
		contracts.Probe:   "cortex.qa.pi-probe/v1",
		contracts.Skill:   "cortex.qa.pi-skill.v1",
		contracts.Result:  "cortex.qa.pi-result.v1",
		contracts.Request: "cortex.qa.pi-admission-request.v1",
	} {
		if got != want {
			t.Fatalf("derived %q, want %q", got, want)
		}
	}
}

// TestModelRelationshipsAreClosed pins the relationship each backend's
// contract carries, and that an unknown backend gets none rather than
// defaulting to the more flattering one.
func TestModelRelationshipsAreClosed(t *testing.T) {
	for backend, want := range map[string]string{
		"pi":       ModelRelationshipRouted,
		"claude":   ModelRelationshipRuntimeFixed,
		"opencode": ModelRelationshipRouted,
	} {
		identity, known := NewBackendIdentity(backend)
		if !known {
			t.Fatalf("NewBackendIdentity(%q) is unknown", backend)
		}
		if identity.Contract != BackendIdentityContract || identity.Backend != backend || identity.ModelRelationship != want {
			t.Fatalf("identity(%q) = %#v, want relationship %q", backend, identity, want)
		}
	}
	if _, known := NewBackendIdentity("unadmitted"); known {
		t.Fatal("NewBackendIdentity accepted a backend route policy does not admit")
	}
}

// TestKnownBackendRequiresBothHalves proves a backend must be admitted by
// route policy and carry a receipt contract. Route policy owns what may run;
// this package owns what may be recorded, and neither half is enough alone.
func TestKnownBackendRequiresBothHalves(t *testing.T) {
	for _, backend := range []string{"pi", "claude", "opencode"} {
		if !qaroute.Admits(backend) || !KnownBackend(backend) {
			t.Fatalf("%q is not admitted and recordable", backend)
		}
	}
	for _, backend := range []string{"", "PI", "gemini", "codex"} {
		if KnownBackend(backend) {
			t.Fatalf("%q is recordable", backend)
		}
	}
}

// TestValidateRequiresTheBackendsOwnIdentity proves a receipt cannot name one
// backend and carry another's identity block or another's contracts.
func TestValidateRequiresTheBackendsOwnIdentity(t *testing.T) {
	t.Run("missing identity block", func(t *testing.T) {
		receipt := testReceipt()
		receipt.BackendIdentity = BackendIdentity{}
		receipt.ReceiptID = ReceiptID(receipt)
		if err := Validate(receipt); err == nil {
			t.Fatal("Validate() accepted a receipt with no backend identity")
		}
	})
	t.Run("another backend's relationship", func(t *testing.T) {
		receipt := testReceipt()
		receipt.BackendIdentity.ModelRelationship = ModelRelationshipRuntimeFixed
		receipt.ReceiptID = ReceiptID(receipt)
		if err := Validate(receipt); err == nil {
			t.Fatal("Validate() accepted a mismatched model relationship")
		}
	})
	t.Run("another backend's name", func(t *testing.T) {
		receipt := testReceipt()
		receipt.BackendIdentity.Backend = "claude"
		receipt.ReceiptID = ReceiptID(receipt)
		if err := Validate(receipt); err == nil {
			t.Fatal("Validate() accepted an identity naming another backend")
		}
	})
	t.Run("another backend's adapter contract", func(t *testing.T) {
		receipt := testReceipt()
		receipt.Versions.Adapter = "cortex.qa.claude-admission.v1"
		receipt.ReceiptID = ReceiptID(receipt)
		if err := Validate(receipt); err == nil {
			t.Fatal("Validate() accepted another backend's adapter contract")
		}
	})
	t.Run("another backend's binary contract", func(t *testing.T) {
		receipt := testReceipt()
		receipt.Binary.Contract = "cortex.qa.claude-binary.v1"
		receipt.ReceiptID = ReceiptID(receipt)
		if err := Validate(receipt); err == nil {
			t.Fatal("Validate() accepted another backend's binary contract")
		}
	})
}

// TestBackendIdentityIsCoveredByTheReceiptID proves the recorded backend
// identity is part of what the receipt ID commits to, so it cannot be edited
// after the fact without invalidating the receipt.
func TestBackendIdentityIsCoveredByTheReceiptID(t *testing.T) {
	receipt := testReceipt()
	original := receipt.ReceiptID
	receipt.BackendIdentity.ModelRelationship = ModelRelationshipRuntimeFixed
	if ReceiptID(receipt) == original {
		t.Fatal("receipt ID ignores the model relationship")
	}
	receipt = testReceipt()
	receipt.BackendIdentity.Contract = "cortex.qa.backend-identity.v2"
	if ReceiptID(receipt) == original {
		t.Fatal("receipt ID ignores the backend identity contract")
	}
}

// TestClaudeReceiptValidatesWithItsOwnContracts proves the whole per-adapter
// contract set holds together for a backend that is not Pi, including the
// runtime-fixed model relationship the accepted limitation requires.
func TestClaudeReceiptValidatesWithItsOwnContracts(t *testing.T) {
	receipt := testReceipt()
	contracts, _ := ContractsFor("claude")
	identity, _ := NewBackendIdentity("claude")
	receipt.Backend = "claude"
	receipt.BackendIdentity = identity
	receipt.Versions.Adapter = contracts.Adapter
	receipt.Versions.SkillContract = contracts.Skill
	receipt.Versions.InputContract = contracts.Input
	receipt.Versions.ProbeContract = contracts.Probe
	receipt.Binary.Contract = contracts.Binary
	receipt.Execution.InvocationContract = contracts.Adapter
	receipt.Route.Resolved.Backend = "claude"
	receipt.ReceiptID = ReceiptID(receipt)
	if err := Validate(receipt); err != nil {
		t.Fatalf("Validate() rejected a claude receipt: %v", err)
	}
	if receipt.BackendIdentity.ModelRelationship != ModelRelationshipRuntimeFixed {
		t.Fatalf("claude relationship = %q, want %q", receipt.BackendIdentity.ModelRelationship, ModelRelationshipRuntimeFixed)
	}
}
