// Package qaadmission defines immutable v1 actor-admission receipt facts.
package qaadmission

import (
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
)

const Contract = "cortex.qa.actor-admission.v1"

type Status string
type Code string

const (
	StatusAdmitted                Status = "admitted"
	StatusNonPassing              Status = "non-passing"
	CodeAdmitted                  Code   = "admitted"
	CodeUnsupportedRole           Code   = "unsupported_role"
	CodeInvalidRequest            Code   = "invalid_request"
	CodeReceiptBoundUnsatisfiable Code   = "receipt_bound_unsatisfiable"
	CodeUnsupportedRuntime        Code   = "unsupported_runtime"
	CodeAdapterUnavailable        Code   = "adapter_unavailable"
	CodeActorUnavailable          Code   = "actor_unavailable"
	CodeActorShadowed             Code   = "actor_shadowed"
	CodeActorDrifted              Code   = "actor_drifted"
	CodeSkillUnavailable          Code   = "skill_unavailable"
	CodeSkillDrifted              Code   = "skill_drifted"
	CodeProfileInvalid            Code   = "profile_invalid"
	CodeRouteIncomplete           Code   = "route_incomplete"
	CodeRouteDisallowed           Code   = "route_disallowed"
	CodeNonNaNRoute               Code   = "non_nan_route"
	CodeModelUnavailable          Code   = "model_unavailable"
	CodeAuthNotReady              Code   = "auth_not_ready"
	CodeBindingStale              Code   = "binding_stale"
	CodeLaunchFailed              Code   = "launch_failed"
	CodeExecutionTimedOut         Code   = "execution_timed_out"
	CodeExecutionFailed           Code   = "execution_failed"
	CodeOutputSilent              Code   = "output_silent"
	CodeOutputTruncated           Code   = "output_truncated"
	CodeNormalizationFailed       Code   = "normalization_failed"
	CodeObservedIdentityMismatch  Code   = "observed_identity_mismatch"
	CodeFallbackObserved          Code   = "fallback_observed"
	CodePolicyViolation           Code   = "policy_violation"
)

var terminalCodes = []Code{
	CodeAdmitted,
	CodeUnsupportedRole,
	CodeInvalidRequest,
	CodeReceiptBoundUnsatisfiable,
	CodeUnsupportedRuntime,
	CodeAdapterUnavailable,
	CodeActorUnavailable,
	CodeActorShadowed,
	CodeActorDrifted,
	CodeSkillUnavailable,
	CodeSkillDrifted,
	CodeProfileInvalid,
	CodeRouteIncomplete,
	CodeRouteDisallowed,
	CodeNonNaNRoute,
	CodeModelUnavailable,
	CodeAuthNotReady,
	CodeBindingStale,
	CodeLaunchFailed,
	CodeExecutionTimedOut,
	CodeExecutionFailed,
	CodeOutputSilent,
	CodeOutputTruncated,
	CodeNormalizationFailed,
	CodeObservedIdentityMismatch,
	CodeFallbackObserved,
	CodePolicyViolation,
}

func TerminalCodes() []Code {
	return append([]Code(nil), terminalCodes...)
}
func IsTerminalCode(code Code) bool {
	for _, candidate := range terminalCodes {
		if candidate == code {
			return true
		}
	}
	return false
}

type Receipt struct {
	Contract     string
	ReceiptID    string
	Status       Status
	Code         Code
	AttemptedRun bool
	Role         qarole.RoleID
	Backend      string
	Versions     Versions
	Installation InstallationIdentity
	Target       TargetIdentity
	Binary       BinaryIdentity
	Route        RouteIdentity
	Availability AvailabilityFacts
	Execution    ExecutionFacts
	Bounds       BoundsFacts
	Diagnostic   *BoundedDiagnostic
}
type Versions struct {
	Receipt, Policy, Profile, Adapter                          string
	ActorContract, SkillContract, InputContract, ProbeContract string
	Runtime                                                    string
}
type InstallationIdentity struct {
	ID                                                                                               installstate.InstallationID
	CatalogSHA256, ActorSourceSHA256, ActorGeneratedSHA256, ActorBindingSHA256, SkillGeneratedSHA256 string
}
type TargetIdentity struct {
	CWDIdentity, Revision, Tree, Fingerprint string
}

const BinaryContract = "cortex.qa.pi-binary.v1"

type BinaryIdentity struct {
	Contract  string
	SHA256    string
	SizeBytes int64
}

type AvailabilityFacts struct {
	Model, Authentication, Fallback string
}
type ExecutionFacts struct {
	InvocationContract, ToolPolicy                             string
	RenderedInputSHA256, Stop, Usage, Completeness, Truncation string
}
type BoundedDiagnostic struct {
	Source, Redaction, ObservedBytes, RetainedBytes string
	RetainedSHA256, Truncation, Completeness        string
}

func NewInstallationIdentity(id installstate.InstallationID, binding qaactor.Binding, sourceSHA256, generatedSHA256, skillSHA256 string) InstallationIdentity {
	return InstallationIdentity{
		ID:                   id,
		CatalogSHA256:        binding.CatalogFingerprint(),
		ActorSourceSHA256:    sourceSHA256,
		ActorGeneratedSHA256: generatedSHA256,
		ActorBindingSHA256:   binding.BindingSHA256(),
		SkillGeneratedSHA256: skillSHA256,
	}
}
