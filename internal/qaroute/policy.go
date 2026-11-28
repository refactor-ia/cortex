package qaroute

import "github.com/refactor-ia/cortex/internal/qarole"

const (
	PolicyVersion   = "cortex.qa.profile-policy.v2"
	ProfileContract = "cortex.qa.nan-profile.v1"
	maxProfileBytes = 256 * 1024
)

type Override struct {
	Provider string
	Model    string
	Effort   string
}
type Request struct {
	Role      qarole.RoleID
	Backend   string
	ProfileID string
	Override  Override
}
type Snapshot struct {
	Present bool
	Bytes   []byte
}
type ResolvedRoute struct {
	PolicyVersion  string
	Role           qarole.RoleID
	Backend        string
	Provider       string
	Model          string
	Effort         string
	ProfileID      string
	ProfileSHA256  string
	OverrideFields []string
}
type Failure struct{ Code string }
type route struct {
	provider string
	model    string
	effort   string
}

// defaults is the closed set of QA roles the policy admits. No role carries a
// provider, model, or effort (D1, #458): a role resolves only through an
// owner's profile route, otherwise it fails route_incomplete.
var defaults = map[qarole.RoleID]route{
	qarole.RequirementsAnalyst: {},
	qarole.TestDesigner:        {},
	qarole.ExploratoryTester:   {},
	qarole.AdversarialTester:   {},
	qarole.TestRunner:          {},
	qarole.EvidenceAuditor:     {},
}

// KnownRole reports whether one role belongs to the closed set the policy
// admits.
func KnownRole(role qarole.RoleID) bool {
	_, known := defaults[role]
	return known
}
