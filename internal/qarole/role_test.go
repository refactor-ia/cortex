package qarole

import (
	"reflect"
	"strings"
	"testing"
)

func TestCatalogContainsExactlyTheSixGeneralCoreRoles(t *testing.T) {
	want := []RoleID{
		RequirementsAnalyst,
		TestDesigner,
		ExploratoryTester,
		AdversarialTester,
		TestRunner,
		EvidenceAuditor,
	}

	contracts := Catalog()
	got := make([]RoleID, len(contracts))
	for index, contract := range contracts {
		got[index] = contract.ID
		if contract.ContractVersion != ContractVersion || len(contract.Criteria) == 0 || len(contract.AllowedActivities) == 0 || len(contract.RequiredStatements) == 0 {
			t.Fatalf("contract %q is incomplete: %+v", contract.ID, contract)
		}
		if strings.ContainsAny(string(contract.ID), "@/") || strings.Contains(string(contract.ID), "model") || strings.Contains(string(contract.ID), "provider") {
			t.Fatalf("role identity %q is not model-neutral", contract.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Catalog() IDs = %q, want %q", got, want)
	}
}

func TestCatalogKeepsRoleResponsibilitiesDistinct(t *testing.T) {
	criteria := make(map[RoleID][]Criterion, len(Catalog()))
	for _, contract := range Catalog() {
		criteria[contract.ID] = contract.Criteria
	}
	for _, tc := range []struct {
		role      RoleID
		criterion Criterion
	}{
		{RequirementsAnalyst, "ambiguity"},
		{TestDesigner, "test-conditions"},
		{ExploratoryTester, "behavior-observations"},
		{AdversarialTester, "failure-behavior"},
		{TestRunner, "test-execution"},
		{EvidenceAuditor, "evidence-sufficiency"},
	} {
		if !containsCriterion(criteria[tc.role], tc.criterion) {
			t.Errorf("role %q is missing its responsibility criterion %q", tc.role, tc.criterion)
		}
	}
}

func TestCatalogAndSquadsReturnIndependentContracts(t *testing.T) {
	catalog := Catalog()
	catalog[0].Criteria[0] = "mutated"

	contracts, err := ValidateSquad([]RoleID{RequirementsAnalyst})
	if err != nil {
		t.Fatalf("ValidateSquad() error = %v", err)
	}
	if contracts[0].Criteria[0] != "ambiguity" {
		t.Fatalf("ValidateSquad() returned shared contract state: %q", contracts[0].Criteria[0])
	}

	contracts[0].RequiredStatements[0] = "mutated"
	if Catalog()[0].RequiredStatements[0] != StatementNeutralBoundedInput {
		t.Fatal("Catalog() returned shared contract state")
	}
}

func containsCriterion(criteria []Criterion, want Criterion) bool {
	for _, criterion := range criteria {
		if criterion == want {
			return true
		}
	}
	return false
}

func TestValidateSquadPreservesOrderAndRejectsIneligibleRoles(t *testing.T) {
	requested := []RoleID{EvidenceAuditor, RequirementsAnalyst}
	contracts, err := ValidateSquad(requested)
	if err != nil {
		t.Fatalf("ValidateSquad() error = %v", err)
	}
	got := []RoleID{contracts[0].ID, contracts[1].ID}
	if !reflect.DeepEqual(got, requested) {
		t.Fatalf("ValidateSquad() IDs = %q, want execution order %q", got, requested)
	}

	for _, squad := range [][]RoleID{
		nil,
		{TestRunner, TestRunner},
		{"qwen-test-runner"},
		{"security-audit"},
		{"frontend-quality"},
		{"flutter-quality"},
	} {
		if contracts, err := ValidateSquad(squad); err == nil || contracts != nil {
			t.Errorf("ValidateSquad(%q) = (%q, nil), want rejection", squad, contracts)
		}
	}
}
