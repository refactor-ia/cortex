package qaroute

import "github.com/refactor-ia/cortex/internal/qarole"

const (
	PolicyVersion   = "cortex.qa.nan-policy.v1"
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

var defaults = map[qarole.RoleID]route{
	qarole.RequirementsAnalyst: {"nan", "qwen3.6", "high"},
	qarole.TestDesigner:        {"nan", "qwen3.6", "high"},
	qarole.ExploratoryTester:   {"nan", "glm5.2", "high"},
	qarole.AdversarialTester:   {"nan", "deepseek-v4-flash", "high"},
	qarole.TestRunner:          {"nan", "qwen3.6", "low"},
	qarole.EvidenceAuditor:     {"nan", "glm5.2", "high"},
}
