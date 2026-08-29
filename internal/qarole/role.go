// Package qarole defines the closed, model-neutral general-core QA role catalog.
package qarole

import (
	"errors"
	"fmt"
)

// RoleID identifies one QA responsibility without provider or model data.
type RoleID string

const (
	// ContractVersion identifies the first general-core role contract.
	ContractVersion = "cortex.qa.role.v1"

	RequirementsAnalyst RoleID = "requirements-analyst"
	TestDesigner        RoleID = "test-designer"
	ExploratoryTester   RoleID = "exploratory-tester"
	AdversarialTester   RoleID = "adversarial-tester"
	TestRunner          RoleID = "test-runner"
	EvidenceAuditor     RoleID = "evidence-auditor"
)

// Criterion identifies one role-specific evaluation concern.
type Criterion string

// Activity identifies an activity allowed by a role contract.
type Activity string

const (
	ActivityEvaluate        Activity = "evaluate"
	ActivityReportEvidence  Activity = "report-evidence"
	ActivityDiagnosticTests Activity = "diagnostic-tests"
)

// StatementID identifies a required safety or evidence statement.
type StatementID string

const (
	StatementNeutralBoundedInput StatementID = "neutral-bounded-input"
	StatementEvidenceOnly        StatementID = "evidence-only-output"
	StatementNoIntegratedFix     StatementID = "no-integrated-product-fix"
	StatementForbiddenActions    StatementID = "forbidden-delivery-and-destructive-actions"
	StatementWorktreeBoundary    StatementID = "worktree-not-hostile-process-isolation"
)

var (
	// ErrEmptySquad reports that a squad has no roles.
	ErrEmptySquad = errors.New("QA squad must contain at least one role")
	// ErrDuplicateRole reports a repeated role in one squad.
	ErrDuplicateRole = errors.New("QA squad contains a duplicate role")
	// ErrUnsupportedRole reports a role outside the closed general-core catalog.
	ErrUnsupportedRole = errors.New("QA role is unsupported")
)

// RoleContract defines the criteria and boundaries for one QA role.
type RoleContract struct {
	ID                 RoleID
	Criteria           []Criterion
	AllowedActivities  []Activity
	RequiredStatements []StatementID
	ContractVersion    string
}

var roleCatalog = []RoleContract{
	{
		ID: RequirementsAnalyst, Criteria: []Criterion{"ambiguity", "completeness", "consistency", "testability"},
		AllowedActivities: standardActivities(), RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
	{
		ID: TestDesigner, Criteria: []Criterion{"test-conditions", "coverage-rationale"},
		AllowedActivities: standardActivities(), RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
	{
		ID: ExploratoryTester, Criteria: []Criterion{"behavior-observations", "reproducibility"},
		AllowedActivities: standardActivities(), RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
	{
		ID: AdversarialTester, Criteria: []Criterion{"assumptions", "boundaries", "failure-behavior"},
		AllowedActivities: standardActivities(), RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
	{
		ID: TestRunner, Criteria: []Criterion{"test-execution", "test-assessment"},
		AllowedActivities: []Activity{ActivityEvaluate, ActivityReportEvidence, ActivityDiagnosticTests}, RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
	{
		ID: EvidenceAuditor, Criteria: []Criterion{"evidence-sufficiency", "attribution", "uncertainty"},
		AllowedActivities: standardActivities(), RequiredStatements: requiredStatements(), ContractVersion: ContractVersion,
	},
}

// Catalog returns independent contracts in canonical general-core order.
func Catalog() []RoleContract {
	contracts := make([]RoleContract, len(roleCatalog))
	for index, contract := range roleCatalog {
		contracts[index] = cloneContract(contract)
	}
	return contracts
}

// ValidateSquad validates a non-empty unique squad and preserves requested order.
func ValidateSquad(requested []RoleID) ([]RoleContract, error) {
	if len(requested) == 0 {
		return nil, ErrEmptySquad
	}

	seen := make(map[RoleID]struct{}, len(requested))
	contracts := make([]RoleContract, 0, len(requested))
	for _, id := range requested {
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateRole, id)
		}
		contract, found := roleContract(id)
		if !found {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedRole, id)
		}
		seen[id] = struct{}{}
		contracts = append(contracts, cloneContract(contract))
	}
	return contracts, nil
}

func roleContract(id RoleID) (RoleContract, bool) {
	for _, contract := range roleCatalog {
		if contract.ID == id {
			return contract, true
		}
	}
	return RoleContract{}, false
}

func standardActivities() []Activity {
	return []Activity{ActivityEvaluate, ActivityReportEvidence}
}

func requiredStatements() []StatementID {
	return []StatementID{
		StatementNeutralBoundedInput,
		StatementEvidenceOnly,
		StatementNoIntegratedFix,
		StatementForbiddenActions,
		StatementWorktreeBoundary,
	}
}

func cloneContract(contract RoleContract) RoleContract {
	contract.Criteria = append([]Criterion(nil), contract.Criteria...)
	contract.AllowedActivities = append([]Activity(nil), contract.AllowedActivities...)
	contract.RequiredStatements = append([]StatementID(nil), contract.RequiredStatements...)
	return contract
}
