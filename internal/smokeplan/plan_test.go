package smokeplan

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildCreatesCanonicalPendingMatrix(t *testing.T) {
	families := approvedFamilies()
	candidates := canonicalCandidates()
	plan, err := Build(Input{
		Catalog:    ApprovedCatalog{Families: families},
		Families:   families,
		Candidates: candidates,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	forecast := plan.Forecast()
	if forecast.Invocations != 33 {
		t.Fatalf("forecast invocations = %d, want 33", forecast.Invocations)
	}
	if !forecast.RequiresExplicitBillingCredentialAuthorization || !forecast.RequiresRealCatalogContent {
		t.Fatalf("forecast = %+v, want explicit billing/credential authorization and real catalog requirements", forecast)
	}

	slots := plan.Slots()
	if len(slots) != 33 {
		t.Fatalf("slot count = %d, want 33", len(slots))
	}
	runtimes := CanonicalRuntimeOrder()
	seen := make(map[RuntimeID]map[FamilyID]bool, len(runtimes))
	for index, slot := range slots {
		candidate := candidates[index/len(families)]
		wantRuntime := runtimes[index/len(families)]
		wantFamily := families[index%len(families)]
		if slot.Ordinal != index+1 || slot.Runtime != wantRuntime || slot.Version != candidate.Version || slot.Family != wantFamily {
			t.Fatalf("slot %d = %+v, want ordinal=%d runtime=%q version=%q family=%q", index, slot, index+1, wantRuntime, candidate.Version, wantFamily)
		}
		if slot.Status != Pending {
			t.Fatalf("slot %d status = %q, want pending", index, slot.Status)
		}
		if seen[slot.Runtime] == nil {
			seen[slot.Runtime] = make(map[FamilyID]bool)
		}
		if seen[slot.Runtime][slot.Family] {
			t.Fatalf("duplicate slot for runtime=%q family=%q", slot.Runtime, slot.Family)
		}
		seen[slot.Runtime][slot.Family] = true
	}
}

func TestBuildRejectsCallerDefinedCatalog(t *testing.T) {
	families := []FamilyID{
		"family-01", "family-02", "family-03", "family-04", "family-05", "family-06",
		"family-07", "family-08", "family-09", "family-10", "family-11",
	}
	_, err := Build(Input{
		Catalog:    ApprovedCatalog{Families: families},
		Families:   approvedFamilies(),
		Candidates: canonicalCandidates(),
	})
	if !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Build() error = %v, want %v", err, ErrUnknownFamily)
	}
}

func TestBuildRejectsInvalidInputs(t *testing.T) {
	valid := Input{Catalog: ApprovedCatalog{Families: approvedFamilies()}, Families: approvedFamilies(), Candidates: canonicalCandidates()}
	cases := []struct {
		name string
		edit func(*Input)
		want error
	}{
		{"missing candidate", func(in *Input) { in.Candidates = in.Candidates[:2] }, ErrMissingRuntime},
		{"unknown runtime", func(in *Input) { in.Candidates[2].Runtime = RuntimeID("other") }, ErrUnknownRuntime},
		{"duplicate runtime", func(in *Input) { in.Candidates[2].Runtime = RuntimePi }, ErrDuplicateRuntime},
		{"duplicate version", func(in *Input) { in.Candidates[2].Version = in.Candidates[0].Version }, ErrDuplicateVersion},
		{"malformed version", func(in *Input) { in.Candidates[0].Version = "v0.84.3" }, ErrMalformedVersion},
		{"noncanonical runtime order", func(in *Input) { in.Candidates[0], in.Candidates[1] = in.Candidates[1], in.Candidates[0] }, ErrNoncanonicalRuntimeOrder},
		{"missing family", func(in *Input) { in.Families = in.Families[:10] }, ErrMissingFamily},
		{"unknown family", func(in *Input) { in.Families[10] = FamilyID("unknown") }, ErrUnknownFamily},
		{"duplicate family", func(in *Input) { in.Families[10] = in.Families[0] }, ErrDuplicateFamily},
		{"noncanonical family order", func(in *Input) { in.Families[0], in.Families[1] = in.Families[1], in.Families[0] }, ErrNoncanonicalFamilyOrder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneInput(valid)
			tc.edit(&in)
			_, err := Build(in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Build() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBuildIsImmutableAndDoesNotCertifyVersions(t *testing.T) {
	input := Input{
		Catalog:  ApprovedCatalog{Families: approvedFamilies()},
		Families: approvedFamilies(),
		Candidates: []CandidateRuntime{
			{Runtime: RuntimePi, Version: "99.99.99"},
			{Runtime: RuntimeOpenCode, Version: "98.98.98"},
			{Runtime: RuntimeClaude, Version: "97.97.97"},
		},
	}
	plan, err := Build(input)
	if err != nil {
		t.Fatalf("Build() error = %v; plan construction must not certify compatibility", err)
	}
	before := plan.Slots()
	order := CanonicalRuntimeOrder()
	order[0] = RuntimeID("mutated-runtime")
	if CanonicalRuntimeOrder()[0] != RuntimePi {
		t.Fatal("canonical runtime order shares mutable storage")
	}

	input.Catalog.Families[0] = "mutated-catalog"
	input.Families[0] = "mutated-request"
	input.Candidates[0].Version = "1.1.1"
	returned := plan.Slots()
	returned[0].Family = "mutated-slot"
	if got := plan.Slots(); !reflect.DeepEqual(got, before) {
		t.Fatalf("plan changed after caller mutation: got %+v, want %+v", got, before)
	}
}

func approvedFamilies() []FamilyID {
	return []FamilyID{
		"reasoning", "model-intelligence", "execution", "quality-assurance", "web",
		"mobile", "pcsoft", "services", "personal", "memory-integration", "documentation",
	}
}

func canonicalCandidates() []CandidateRuntime {
	return []CandidateRuntime{
		{Runtime: RuntimePi, Version: "0.84.3"},
		{Runtime: RuntimeOpenCode, Version: "1.18.21"},
		{Runtime: RuntimeClaude, Version: "2.1.243"},
	}
}

func cloneInput(input Input) Input {
	input.Catalog.Families = append([]FamilyID(nil), input.Catalog.Families...)
	input.Families = append([]FamilyID(nil), input.Families...)
	input.Candidates = append([]CandidateRuntime(nil), input.Candidates...)
	return input
}
