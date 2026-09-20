package qapi

import "github.com/refactor-ia/cortex/internal/qaadmission"

// Every contract a QA backend records is per-adapter. The helpers below are
// the qapi-side readers of that one table, which qaadmission owns because the
// receipt is what the contracts have to agree with.
//
// They return the empty string for a backend the route policy does not admit.
// That is deliberate and safe: an empty contract matches nothing the receipt
// validator, the input frame, or the result echo will accept, so a backend
// that was never admitted fails closed rather than borrowing Pi's identity.

func contractsFor(backend string) qaadmission.Contracts {
	contracts, _ := qaadmission.ContractsFor(backend)
	return contracts
}

// piContracts is the Pi adapter's own contract set, for the Pi-only paths that
// have no route in hand.
func piContracts() qaadmission.Contracts {
	return contractsFor(piBackendID)
}

// InputContractFor is the bounded-input contract one backend frames with. It
// is exported because the framed identity block names it and a caller
// verifying a frame needs the same value.
func InputContractFor(backend string) string {
	return contractsFor(backend).Input
}

func skillContractFor(backend string) string {
	return contractsFor(backend).Skill
}

func resultContractFor(backend string) string {
	return contractsFor(backend).Result
}

// AdmissionRequestContractFor is the decoded-request contract one backend's
// admission request must declare.
func AdmissionRequestContractFor(backend string) string {
	return contractsFor(backend).Request
}
