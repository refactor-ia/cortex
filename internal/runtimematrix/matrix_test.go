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
			name: "present uncertified runtimes are admitted by default",
			observations: []Observation{
				{ID: RuntimePi, Present: true, Version: "0.1.0", Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: true, Version: "0.2.0", Compatibility: CompatibilityUnknown},
				{ID: RuntimeClaudeCode, Present: true, Version: "0.3.0", Compatibility: CompatibilityUnknown},
			},
			want: []Decision{
				{ID: RuntimePi, Outcome: OutcomePresentUncertified, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
				{ID: RuntimeOpenCode, Outcome: OutcomePresentUncertified, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
				{ID: RuntimeClaudeCode, Outcome: OutcomePresentUncertified, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
			},
			compatible: true,
		},
		{
			name: "only a known-incompatible adapter is refused while others apply",
			observations: []Observation{
				{ID: RuntimePi, Present: true, Version: "0.1.0", Compatibility: CompatibilityUnknown},
				{ID: RuntimeOpenCode, Present: true, Version: "0.2.0", Compatibility: Incompatible},
				{ID: RuntimeClaudeCode, Present: true, Version: "0.3.0", Compatibility: Compatible},
			},
			want: []Decision{
				{ID: RuntimePi, Outcome: OutcomePresentUncertified, Action: Configure, IncludeInTransaction: true, TouchAllowed: true},
				{ID: RuntimeOpenCode, Outcome: OutcomeKnownIncompatible, Action: Skip, IncludeInTransaction: false, TouchAllowed: false},
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

func TestDecideLocalUpdateAdmitsOnlyTheSelectedRuntime(t *testing.T) {
	observations := []Observation{
		{ID: RuntimePi, Present: true, Version: "0.85.1", Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: true, Version: "1.18.21", Compatibility: Incompatible},
		{ID: RuntimeClaudeCode, Present: true, Version: "2.1.251", Compatibility: CompatibilityUnknown},
	}
	// The default decision admits both uncertified runtimes; the local update
	// must still narrow the transaction to exactly one selected target.
	base, err := Decide(observations)
	if err != nil || len(transactionTargets(base)) != 2 {
		t.Fatalf("default decision = (%#v, %v)", base, err)
	}
	local, err := DecideLocalUpdate(observations, RuntimePi)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.Decisions[0]; got.Outcome != OutcomePresentUncertified || got.Action != Configure || !got.IncludeInTransaction || !got.TouchAllowed {
		t.Fatalf("local selected decision = %#v", got)
	}
	if got := transactionTargets(local); len(got) != 1 || got[0] != RuntimePi {
		t.Fatalf("local transaction targets = %#v, want exactly the selected runtime", got)
	}
	// A known-incompatible target stays refused; an admissible one is the only
	// runtime the narrowed matrix includes.
	if matrix, err := DecideLocalUpdate(observations, RuntimeOpenCode); err != nil || matrix.HasCompatible {
		t.Fatalf("known-incompatible local target = (%#v, %v)", matrix, err)
	}
	if matrix, err := DecideLocalUpdate(observations, RuntimeClaudeCode); err != nil || !matrix.HasCompatible || len(transactionTargets(matrix)) != 1 {
		t.Fatalf("selected local target = (%#v, %v)", matrix, err)
	}
}

func TestDecideLocalUpdateRefusesAnUnparseableSelectedVersion(t *testing.T) {
	matrix, err := DecideLocalUpdate([]Observation{
		{ID: RuntimePi, Present: true, Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
		{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
	}, RuntimePi)
	if err != nil {
		t.Fatal(err)
	}
	if matrix.HasCompatible || matrix.Decisions[0].Outcome != OutcomeUnknownVersion {
		t.Fatalf("unparseable local target = %#v", matrix)
	}
}

func TestDecideLocalUpdateRejectsAnUnsupportedTarget(t *testing.T) {
	observations := []Observation{
		{ID: RuntimePi, Present: true, Version: "0.85.1", Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: false, Compatibility: CompatibilityUnknown},
		{ID: RuntimeClaudeCode, Present: false, Compatibility: CompatibilityUnknown},
	}
	if matrix, err := DecideLocalUpdate(observations, RuntimeID("other")); err == nil || !reflect.DeepEqual(matrix, Matrix{}) {
		t.Fatalf("unsupported local target = (%#v, %v)", matrix, err)
	}
}

func TestDecideRefusesPresentRuntimeWithoutAnIdentifiedVersion(t *testing.T) {
	matrix, err := Decide([]Observation{
		{ID: RuntimePi, Present: true, Compatibility: CompatibilityUnknown},
		{ID: RuntimeOpenCode, Present: true, Compatibility: CompatibilityUnknown},
		{ID: RuntimeClaudeCode, Present: true, Compatibility: CompatibilityUnknown},
	})
	if err != nil {
		t.Fatal(err)
	}
	if matrix.HasCompatible {
		t.Fatalf("unidentified runtimes were admitted: %#v", matrix)
	}
	for _, decision := range matrix.Decisions {
		if decision.Outcome != OutcomeUnknownVersion || decision.Action != Warn || decision.IncludeInTransaction || decision.TouchAllowed {
			t.Fatalf("unidentified decision = %#v", decision)
		}
	}
}

func transactionTargets(matrix Matrix) []RuntimeID {
	targets := make([]RuntimeID, 0, len(matrix.Decisions))
	for _, decision := range matrix.Decisions {
		if decision.IncludeInTransaction {
			targets = append(targets, decision.ID)
		}
	}
	return targets
}
