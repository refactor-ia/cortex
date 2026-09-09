package qaadmission

import "strconv"

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
func MinimumSize() int {
	maximum := 0
	for _, code := range terminalCodes {
		receipt := Receipt{Contract: Contract, Code: code, Bounds: FixedBounds()}
		if code == CodeAdmitted {
			receipt.Status = StatusAdmitted
		} else {
			receipt.Status = StatusNonPassing
		}
		size := 8 + len("receipt.") + 64
		for _, field := range receiptFields(receipt) {
			size += 8 + len(field)
		}
		if size > maximum {
			maximum = size
		}
	}
	return maximum
}
func BoundsSatisfiable(size int) bool {
	return size >= MinimumSize() && size <= MaxReceiptBytes
}
func PrelaunchCode(size int) Code {
	if !BoundsSatisfiable(size) {
		return CodeReceiptBoundUnsatisfiable
	}
	return CodeAdmitted
}
