package qaadmission

import (
	"encoding/json"
	"fmt"
	"strconv"
)

const (
	MaxRequestBytes         = 128 * 1024
	MaxTaskBytes            = 64 * 1024
	MaxProfileSnapshotBytes = 256 * 1024
	MaxStdoutBytes          = 1024 * 1024
	MaxStderrBytes          = 256 * 1024
	MaxReceiptBytes         = 512 * 1024
	MaxDiagnosticBytes      = 16 * 1024
	DefaultTimeoutSeconds   = 900
	MinimumTimeoutSeconds   = 30
	MaximumTimeoutSeconds   = 3600
)

type BoundsFacts struct {
	RequestBytes, TaskBytes, ProfileBytes  int
	StdoutBytes, StderrBytes, ReceiptBytes int
	DiagnosticBytes, TimeoutSeconds        int
}

func FixedBounds() BoundsFacts {
	return BoundsFacts{
		RequestBytes:    MaxRequestBytes,
		TaskBytes:       MaxTaskBytes,
		ProfileBytes:    MaxProfileSnapshotBytes,
		StdoutBytes:     MaxStdoutBytes,
		StderrBytes:     MaxStderrBytes,
		ReceiptBytes:    MaxReceiptBytes,
		DiagnosticBytes: MaxDiagnosticBytes,
		TimeoutSeconds:  DefaultTimeoutSeconds,
	}
}

func BoundsForTimeout(timeout int) (BoundsFacts, bool) {
	if timeout < MinimumTimeoutSeconds || timeout > MaximumTimeoutSeconds {
		return BoundsFacts{}, false
	}
	bounds := FixedBounds()
	bounds.TimeoutSeconds = timeout
	return bounds, true
}

func validBounds(bounds BoundsFacts) bool {
	want, ok := BoundsForTimeout(bounds.TimeoutSeconds)
	return ok && bounds == want
}
func (bounds BoundsFacts) fields() []string {
	return []string{
		strconv.Itoa(bounds.RequestBytes),
		strconv.Itoa(bounds.TaskBytes),
		strconv.Itoa(bounds.ProfileBytes),
		strconv.Itoa(bounds.StdoutBytes),
		strconv.Itoa(bounds.StderrBytes),
		strconv.Itoa(bounds.ReceiptBytes),
		strconv.Itoa(bounds.DiagnosticBytes),
		strconv.Itoa(bounds.TimeoutSeconds),
	}
}
func MinimumSize(basis Receipt) (int, error) {
	basis.Status = ""
	basis.Code = ""
	basis.AttemptedRun = false
	basis.ReceiptID = ""
	basis.Availability = AvailabilityFacts{}
	basis.Execution = ExecutionFacts{}
	basis.Diagnostic = nil
	basis.Route.Observed = ObservedIdentity{Effort: UnobservableEffort()}

	if basis.Contract != Contract || basis.Backend != "pi" {
		return unsatisfiableSize(basis), fmt.Errorf("invalid receipt basis")
	}
	if err := validateIdentity(basis); err != nil {
		return unsatisfiableSize(basis), err
	}
	if err := validateRoute(basis); err != nil {
		return unsatisfiableSize(basis), err
	}

	maximum := 0
	for _, code := range terminalCodes {
		for _, attempted := range allowedAttempts(code) {
			candidate := minimumCandidate(basis, code, attempted)
			if err := Validate(candidate); err != nil {
				return unsatisfiableSize(basis), err
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				return unsatisfiableSize(basis), err
			}
			if len(encoded) > maximum {
				maximum = len(encoded)
			}
		}
	}
	return maximum, nil
}

func minimumCandidate(basis Receipt, code Code, attempted bool) Receipt {
	candidate := basis
	candidate.Status = StatusNonPassing
	candidate.Code = code
	candidate.AttemptedRun = attempted
	candidate.ReceiptID = ""
	candidate.Availability = AvailabilityFacts{}
	candidate.Execution = ExecutionFacts{}
	candidate.Diagnostic = nil
	candidate.Route.Observed = ObservedIdentity{Effort: UnobservableEffort()}
	if code == CodeAdmitted {
		candidate.Status = StatusAdmitted
		candidate.Availability = AvailabilityFacts{Model: "available", Authentication: "ready", Fallback: "none"}
		candidate.Execution = ExecutionFacts{InvocationContract: "cortex.qa.pi-admission.v1", ToolPolicy: "read,grep,find,ls", RenderedInputSHA256: "0000000000000000000000000000000000000000000000000000000000000000", Stop: "none", Usage: "unavailable", Completeness: "complete", Truncation: "none"}
		candidate.Route.Observed.Provider = candidate.Route.Resolved.Provider
		candidate.Route.Observed.Model = candidate.Route.Resolved.Model
	}
	candidate.ReceiptID = ReceiptID(candidate)
	return candidate
}

func allowedAttempts(code Code) []bool {
	if mustAttempt(code) {
		return []bool{true}
	}
	if mayAttempt(code) {
		return []bool{false, true}
	}
	return []bool{false}
}

func unsatisfiableSize(receipt Receipt) int {
	maximumInt := int(^uint(0) >> 1)
	if receipt.Bounds.ReceiptBytes < maximumInt {
		return receipt.Bounds.ReceiptBytes + 1
	}
	return maximumInt
}

func BoundsSatisfiable(receipt Receipt, size int) bool {
	minimum, err := MinimumSize(receipt)
	return err == nil && size >= minimum && size <= receipt.Bounds.ReceiptBytes
}
func PrelaunchCode(receipt Receipt, size int) Code {
	if !BoundsSatisfiable(receipt, size) {
		return CodeReceiptBoundUnsatisfiable
	}
	return CodeAdmitted
}
