package qaadmission

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestBoundsForTimeout(t *testing.T) {
	fixed := FixedBounds()
	for _, tc := range []struct {
		name    string
		timeout int
		valid   bool
	}{
		{"minimum", MinimumTimeoutSeconds, true},
		{"default", DefaultTimeoutSeconds, true},
		{"maximum", MaximumTimeoutSeconds, true},
		{"below minimum", MinimumTimeoutSeconds - 1, false},
		{"above maximum", MaximumTimeoutSeconds + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bounds, ok := BoundsForTimeout(tc.timeout)
			if ok != tc.valid {
				t.Fatalf("BoundsForTimeout(%d) valid = %t, want %t", tc.timeout, ok, tc.valid)
			}
			if !ok {
				return
			}
			if bounds.TimeoutSeconds != tc.timeout {
				t.Fatalf("BoundsForTimeout(%d) timeout = %d", tc.timeout, bounds.TimeoutSeconds)
			}
			bounds.TimeoutSeconds = fixed.TimeoutSeconds
			if bounds != fixed {
				t.Fatalf("BoundsForTimeout(%d) changed fixed caps: %#v", tc.timeout, bounds)
			}
		})
	}
}

func TestBoundsForTimeoutDefaultKeepsCanonicalJSON(t *testing.T) {
	receipt := testReceipt()
	baseline, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatalf("CanonicalJSON() baseline: %v", err)
	}
	bounds, ok := BoundsForTimeout(DefaultTimeoutSeconds)
	if !ok {
		t.Fatal("BoundsForTimeout(default) rejected the default timeout")
	}
	receipt.Bounds = bounds
	receipt.ReceiptID = ReceiptID(receipt)
	got, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatalf("CanonicalJSON() default timeout bounds: %v", err)
	}
	if !bytes.Equal(got, baseline) {
		t.Fatalf("CanonicalJSON() changed for the default timeout\n got: %s\nwant: %s", got, baseline)
	}
}

func TestFixedBoundsAndMinimumSize(t *testing.T) {
	bounds := FixedBounds()
	if bounds.RequestBytes != 128*1024 || bounds.TaskBytes != 64*1024 || bounds.ReceiptBytes != 512*1024 || bounds.DiagnosticBytes != 16*1024 {
		t.Fatal("bootstrap bounds changed")
	}
	receipt := testReceipt()
	minimum, err := MinimumSize(receipt)
	if err != nil {
		t.Fatalf("MinimumSize(): %v", err)
	}
	if minimum > receipt.Bounds.ReceiptBytes || !BoundsSatisfiable(receipt, minimum) || BoundsSatisfiable(receipt, minimum-1) {
		t.Fatal("minimum receipt size does not cover all terminal envelopes")
	}
	if PrelaunchCode(receipt, minimum-1) != CodeReceiptBoundUnsatisfiable {
		t.Fatal("one byte below minimum did not fail before launch")
	}
	admitted := receipt
	admitted.Status, admitted.Code, admitted.AttemptedRun = StatusAdmitted, CodeAdmitted, true
	admitted.Availability = AvailabilityFacts{}
	admitted.Execution = ExecutionFacts{}
	admitted.Route.Observed = ObservedIdentity{Effort: UnobservableEffort()}
	admitted.ReceiptID = ReceiptID(admitted)
	if _, err := CanonicalJSON(admitted); err == nil {
		t.Fatal("CanonicalJSON accepted admitted receipt without evidence")
	}
}

func TestMinimumSizeTerminalFreeBasis(t *testing.T) {
	basis := testReceipt()
	want := independentMinimumSize(t, basis)
	basis.Status = "not-a-status"
	basis.Code = "not-a-code"
	basis.AttemptedRun = true
	basis.Availability = AvailabilityFacts{Model: "bad\u2028value"}
	basis.Execution = ExecutionFacts{Stop: "bad\u2028value"}
	basis.Diagnostic = &BoundedDiagnostic{Source: strings.Repeat("\u2028", 64)}
	basis.ReceiptID = "receipt.not-current"

	got, err := MinimumSize(basis)
	if err != nil {
		t.Fatalf("MinimumSize() terminal-free basis: %v", err)
	}
	if got != want {
		t.Fatalf("MinimumSize() = %d, want independent maximum %d", got, want)
	}
}

func TestMinimumSizeRequiresMandatoryIdentity(t *testing.T) {
	basis := testReceipt()
	basis.Installation.ID = "invalid"
	got, err := MinimumSize(basis)
	if err == nil {
		t.Fatal("MinimumSize() accepted invalid mandatory identity")
	}
	if got <= basis.Bounds.ReceiptBytes {
		t.Fatalf("MinimumSize() error size = %d, want above receipt bound %d", got, basis.Bounds.ReceiptBytes)
	}
	if BoundsSatisfiable(basis, got) {
		t.Fatal("BoundsSatisfiable() accepted an unsatisfiable basis")
	}
	if PrelaunchCode(basis, got) != CodeReceiptBoundUnsatisfiable {
		t.Fatal("PrelaunchCode() changed receipt_bound_unsatisfiable semantics")
	}
}

func TestMinimumSizeUsesActualTimeout(t *testing.T) {
	for _, timeout := range []int{MinimumTimeoutSeconds, DefaultTimeoutSeconds, MaximumTimeoutSeconds} {
		t.Run(strconv.Itoa(timeout), func(t *testing.T) {
			basis := testReceipt()
			bounds, ok := BoundsForTimeout(timeout)
			if !ok {
				t.Fatalf("BoundsForTimeout(%d) rejected timeout", timeout)
			}
			basis.Bounds = bounds
			want := independentMinimumSize(t, basis)
			got, err := MinimumSize(basis)
			if err != nil {
				t.Fatalf("MinimumSize() timeout %d: %v", timeout, err)
			}
			if got != want {
				t.Fatalf("MinimumSize() timeout %d = %d, want %d", timeout, got, want)
			}
		})
	}
}

func TestMinimumSizeValidatesEveryTerminalCandidateAndUsesJSONBytes(t *testing.T) {
	basis := testReceipt()
	want := independentMinimumSize(t, basis)
	got, err := MinimumSize(basis)
	if err != nil {
		t.Fatalf("MinimumSize(): %v", err)
	}
	if got != want {
		t.Fatalf("MinimumSize() = %d, want independent maximum %d", got, want)
	}
}

func independentMinimumSize(t *testing.T, basis Receipt) int {
	t.Helper()
	maximum := 0
	for _, code := range TerminalCodes() {
		valid := 0
		for _, attempted := range []bool{false, true} {
			candidate := independentCandidate(basis, code, attempted)
			if err := Validate(candidate); err != nil {
				continue
			}
			valid++
			encoded, err := json.Marshal(candidate)
			if err != nil {
				t.Fatalf("json.Marshal(%s, attempted=%t): %v", code, attempted, err)
			}
			if len(encoded) > maximum {
				maximum = len(encoded)
			}
		}
		if valid == 0 {
			t.Fatalf("no valid minimal candidate for terminal code %q", code)
		}
	}
	return maximum
}

func independentCandidate(basis Receipt, code Code, attempted bool) Receipt {
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
		candidate.Execution = ExecutionFacts{InvocationContract: "cortex.qa.pi-admission.v1", ToolPolicy: "read,grep,find,ls", RenderedInputSHA256: strings.Repeat("0", 64), Stop: "none", Usage: "unavailable", Completeness: "complete", Truncation: "none"}
		candidate.Route.Observed.Provider = candidate.Route.Resolved.Provider
		candidate.Route.Observed.Model = candidate.Route.Resolved.Model
	}
	candidate.ReceiptID = ReceiptID(candidate)
	return candidate
}
