package runtimematrix

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name         string
		observations []Observation
		want         []Decision
		compatible   bool
	}{
		{
			name: "mixed runtime states",
			observations: []Observation{
				{ID: RuntimeClaudeCode, Present: true, Version: "1.2.3", Compatibility: Compatible},
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: Incompatible},
			},
			want: []Decision{
				{ID: RuntimePi, Outcome: OutcomeAbsent, Action: Warn, IncludeInTransaction: false, TouchAllowed: false},
				{ID: RuntimeOpenCode, Outcome: OutcomeKnownIncompatible, Action: Skip, IncludeInTransaction: false, TouchAllowed: false},
				{ID: RuntimeClaudeCode, Outcome: OutcomePresentCompatible, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
			},
			compatible: true,
		},
		{
			name: "all compatible",
			observations: []Observation{
				{ID: RuntimePi, Present: true, Version: "1.0.0", Compatibility: Compatible},
				{ID: RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: Compatible},
				{ID: RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: Compatible},
			},
			want: []Decision{
				{ID: RuntimePi, Outcome: OutcomePresentCompatible, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
				{ID: RuntimeOpenCode, Outcome: OutcomePresentCompatible, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
				{ID: RuntimeClaudeCode, Outcome: OutcomePresentCompatible, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
			},
			compatible: true,
		},
		{
			name: "no compatible runtimes",
			observations: []Observation{
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: Incompatible},
				{ID: RuntimeClaudeCode, Present: true, Compatibility: CompatibilityUnknown},
			},
			want: []Decision{
				{ID: RuntimePi, Outcome: OutcomeAbsent, Action: Warn, IncludeInTransaction: false, TouchAllowed: false},
				{ID: RuntimeOpenCode, Outcome: OutcomeKnownIncompatible, Action: Skip, IncludeInTransaction: false, TouchAllowed: false},
				{ID: RuntimeClaudeCode, Outcome: OutcomeUnknownVersion, Action: Warn, IncludeInTransaction: false, TouchAllowed: false},
			},
			compatible: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix, err := Decide(tt.observations)
			if err != nil {
				t.Fatalf("Decide() error = %v", err)
			}
			if !reflect.DeepEqual(matrix.Decisions, tt.want) {
				t.Errorf("Decisions = %#v, want %#v", matrix.Decisions, tt.want)
			}
			if matrix.HasCompatible != tt.compatible {
				t.Errorf("HasCompatible = %t, want %t", matrix.HasCompatible, tt.compatible)
			}
			for _, decision := range matrix.Decisions {
				if decision.Action != Configure && (decision.IncludeInTransaction || decision.TouchAllowed) {
					t.Errorf("non-compatible decision %#v authorizes a transaction", decision)
				}
			}
		})
	}
}

func TestDecideIsIndependentOfObservationOrder(t *testing.T) {
	observations := []Observation{
		{ID: RuntimeClaudeCode, Present: true, Version: "3.0.0", Compatibility: Compatible},
		{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: true, Version: "2.0.0", Compatibility: Incompatible},
	}

	matrix, err := Decide(observations)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if got := matrix.Decisions; !reflect.DeepEqual(got, []Decision{
		{ID: RuntimePi, Outcome: OutcomeAbsent, Action: Warn},
		{ID: RuntimeOpenCode, Outcome: OutcomeKnownIncompatible, Action: Skip},
		{ID: RuntimeClaudeCode, Outcome: OutcomePresentCompatible, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
	}) {
		t.Errorf("Decisions = %#v, want deterministic runtime order", got)
	}
}

func TestDecideLocalUpdateKeepsStrictCertificationSeparate(t *testing.T) {
	observations := []Observation{
		{ID: RuntimePi, Present: true, Version: "0.85.1", Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: true, Version: "1.18.21", Compatibility: Incompatible},
		{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
	}
	strict, err := Decide(observations)
	if err != nil || strict.HasCompatible || strict.Decisions[0].Outcome != OutcomeUnknownVersion || strict.Decisions[0].IncludeInTransaction {
		t.Fatalf("strict decision = (%#v, %v)", strict, err)
	}
	local, err := DecideLocalUpdate(observations, RuntimePi)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Decisions[0]; got.Outcome != OutcomePresentUncertified || got.Action != Configure || !got.IncludeInTransaction || !got.TouchAllowed {
		t.Fatalf("local selected decision = %#v", got)
	}
	for _, index := range []int{1, 2} {
		if local.Decisions[index].IncludeInTransaction || local.Decisions[index].TouchAllowed {
			t.Fatalf("local decision authorized non-target %#v", local.Decisions[index])
		}
	}
	for _, target := range []RuntimeID{RuntimeOpenCode, RuntimeClaudeCode} {
		matrix, err := DecideLocalUpdate(observations, target)
		if err != nil || matrix.HasCompatible {
			t.Fatalf("ineligible local target %q = (%#v, %v)", target, matrix, err)
		}
	}
}

func TestDecideRejectsInvalidObservations(t *testing.T) {
	tests := []struct {
		name         string
		observations []Observation
		wantError    string
	}{
		{
			name: "unknown runtime",
			observations: []Observation{
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeID("other"), Present: false, Compatibility: CompatibilityUnknown},
			},
			wantError: "unknown runtime",
		},
		{
			name: "duplicate runtime",
			observations: []Observation{
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
			},
			wantError: "duplicate runtime",
		},
		{
			name: "missing runtime",
			observations: []Observation{
				{ID: RuntimePi, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
			},
			wantError: "missing runtime",
		},
		{
			name: "absent runtime has version",
			observations: []Observation{
				{ID: RuntimePi, Present: false, Version: "1.0.0", Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
			},
			wantError: "absent runtime cannot have a version",
		},
		{
			name: "unknown version has compatibility",
			observations: []Observation{
				{ID: RuntimePi, Present: true, Compatibility: Compatible},
				{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
				{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
			},
			wantError: "unknown version cannot have adapter compatibility",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix, err := Decide(tt.observations)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Errorf("Decide() error = %v, want %q", err, tt.wantError)
			}
			if !reflect.DeepEqual(matrix, Matrix{}) {
				t.Errorf("Decide() matrix = %#v, want zero Matrix on validation error", matrix)
			}
		})
	}
}
