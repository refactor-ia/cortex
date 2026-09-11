package qaadmission

import (
	"encoding/json"
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
func MinimumSize(receipt Receipt) int {
	maximum := 0
	for _, code := range terminalCodes {
		candidate := receipt
		candidate.Code = code
		candidate.Status = StatusNonPassing
		candidate.AttemptedRun = mustAttempt(code)
		if code == CodeAdmitted {
			candidate.Status = StatusAdmitted
			candidate.AttemptedRun = true
		}
		candidate.ReceiptID = ReceiptID(candidate)
		encoded, err := json.Marshal(candidate)
		if err != nil {
			return MaxReceiptBytes + 1
		}
		if len(encoded) > maximum {
			maximum = len(encoded)
		}
	}
	return maximum
}
func BoundsSatisfiable(receipt Receipt, size int) bool {
	return size >= MinimumSize(receipt) && size <= receipt.Bounds.ReceiptBytes
}
func PrelaunchCode(receipt Receipt, size int) Code {
	if !BoundsSatisfiable(receipt, size) {
		return CodeReceiptBoundUnsatisfiable
	}
	return CodeAdmitted
}
