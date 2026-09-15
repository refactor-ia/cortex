package qarole

import (
	"fmt"
	"strings"
)

const diagnosticMutationMarker = "diagnostic-mutation-confirmed-disposable-worktree"

// ValidateSourceContract verifies the required machine-readable QA source markers.
func ValidateSourceContract(contract RoleContract, source string) error {
	official, found := roleContract(contract.ID)
	if !found {
		return fmt.Errorf("QA source contract has an unsupported role")
	}
	for _, marker := range sourceMarkers(official) {
		if !strings.Contains(source, "<!-- cortex-qa:"+marker+" -->") {
			return fmt.Errorf("QA source contract is missing a required marker")
		}
	}
	return nil
}

func sourceMarkers(contract RoleContract) []string {
	criteria := make([]string, len(contract.Criteria))
	for index, criterion := range contract.Criteria {
		criteria[index] = string(criterion)
	}
	markers := []string{
		"role-id=" + string(contract.ID),
		"role-criteria=" + strings.Join(criteria, ","),
	}
	for _, statement := range contract.RequiredStatements {
		markers = append(markers, string(statement))
	}
	return append(markers, diagnosticMutationMarker)
}
