package qaadmission

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

var (
	hex64     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	id32      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	oid       = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	cwd       = regexp.MustCompile(`^cwd\.[0-9a-f]{64}$`)
	fp        = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[0-9a-f]{64}$`)
	profileID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	redaction = regexp.MustCompile(`^(?:none|credential-assignment(?:,authorization-bearer(?:,authorization-basic(?:,private-key)?)?)?|authorization-bearer(?:,authorization-basic(?:,private-key)?)?|authorization-basic(?:,private-key)?|private-key)$`)
)

func Validate(receipt Receipt) error {
	if receipt.Contract != Contract || receipt.Versions.Receipt != Contract || receipt.Backend != "pi" || !IsTerminalCode(receipt.Code) || (receipt.Status == StatusAdmitted) != (receipt.Code == CodeAdmitted) || (receipt.Status != StatusAdmitted && receipt.Status != StatusNonPassing) || (mustAttempt(receipt.Code) && !receipt.AttemptedRun) || (!mayAttempt(receipt.Code) && receipt.AttemptedRun) {
		return fmt.Errorf("invalid terminal receipt")
	}
	if err := validateIdentity(receipt); err != nil {
		return err
	}
	if err := validateRoute(receipt); err != nil {
		return err
	}
	if !oneOf(receipt.Availability.Model, "", "available", "unavailable") || !oneOf(receipt.Availability.Authentication, "", "ready", "not-ready") || !oneOf(receipt.Availability.Fallback, "", "none", "observed") {
		return fmt.Errorf("invalid availability facts")
	}
	if err := validateExecution(receipt.Execution); err != nil {
		return err
	}
	if receipt.Diagnostic != nil {
		if err := validateDiagnostic(*receipt.Diagnostic, receipt.Bounds); err != nil {
			return err
		}
	}
	if receipt.ReceiptID != ReceiptID(receipt) {
		return fmt.Errorf("invalid receipt ID")
	}
	if receipt.Status == StatusAdmitted && !hasAdmittedEvidence(receipt) {
		return fmt.Errorf("incomplete admitted evidence")
	}
	return nil
}

func CanonicalJSON(receipt Receipt) ([]byte, error) {
	if err := Validate(receipt); err != nil {
		return nil, err
	}
	return json.Marshal(receipt)
}

func validateIdentity(receipt Receipt) error {
	if _, err := qarole.ValidateSquad([]qarole.RoleID{receipt.Role}); err != nil || !id32.MatchString(string(receipt.Installation.ID)) || !cwd.MatchString(receipt.Target.CWDIdentity) || !oid.MatchString(receipt.Target.Revision) || !oid.MatchString(receipt.Target.Tree) || !fp.MatchString(receipt.Target.Fingerprint) || receipt.Binary.Contract != BinaryContract || !hex64.MatchString(receipt.Binary.SHA256) || receipt.Binary.SizeBytes <= 0 || receipt.Bounds != FixedBounds() || receipt.Route.Observed.Effort != UnobservableEffort() {
		return fmt.Errorf("invalid bound identity")
	}
	for _, value := range []string{receipt.Installation.CatalogSHA256, receipt.Installation.ActorSourceSHA256, receipt.Installation.ActorGeneratedSHA256, receipt.Installation.ActorBindingSHA256, receipt.Installation.SkillGeneratedSHA256} {
		if !hex64.MatchString(value) {
			return fmt.Errorf("invalid identity hash")
		}
	}
	if !validVersions(receipt.Versions) {
		return fmt.Errorf("invalid receipt versions")
	}
	return nil
}
func validVersions(versions Versions) bool {
	return versions.Receipt == Contract && versions.Policy == qaroute.PolicyVersion && versions.Profile == qaroute.ProfileContract && versions.Adapter == "cortex.qa.pi-admission.v1" && versions.ActorContract == qaactor.ActorContractVersion && versions.SkillContract == "cortex.qa.pi-skill.v1" && versions.InputContract == "cortex.qa.pi-input.v1" && versions.ProbeContract == "cortex.qa.pi-probe/v1" && versions.Runtime == "0.84.4"
}
func validateRoute(receipt Receipt) error {
	route := receipt.Route.Resolved
	want, failure := qaroute.Resolve(qaroute.Request{Role: receipt.Role, Backend: receipt.Backend}, qaroute.Snapshot{})
	if failure.Code != "" || route.PolicyVersion != qaroute.PolicyVersion || route.Role != receipt.Role || route.Backend != receipt.Backend || route.Provider != want.Provider || route.Model != want.Model || route.Effort != want.Effort {
		return fmt.Errorf("invalid resolved route")
	}
	if route.ProfileID == "role-default" {
		if route.ProfileSHA256 != "" {
			return fmt.Errorf("invalid default profile")
		}
	} else if !profileID.MatchString(route.ProfileID) || !hex64.MatchString(route.ProfileSHA256) {
		return fmt.Errorf("invalid selected profile")
	}
	requested := receipt.Route.Requested
	values := []struct {
		name string
		got  string
		want string
	}{
		{"provider", requested.Provider, route.Provider},
		{"model", requested.Model, route.Model},
		{"effort", requested.Effort, route.Effort},
	}
	fields := make([]string, 0, len(values))
	for _, value := range values {
		if value.got != "" {
			if value.got != value.want {
				return fmt.Errorf("invalid requested route")
			}
			fields = append(fields, value.name)
		}
	}
	if strings.Join(route.OverrideFields, ",") != strings.Join(fields, ",") {
		return fmt.Errorf("invalid override fields")
	}
	observed := receipt.Route.Observed
	if (observed.Provider == "") != (observed.Model == "") {
		return fmt.Errorf("invalid observed identity")
	}
	return nil
}
func validateExecution(facts ExecutionFacts) error {
	if !oneOf(facts.InvocationContract, "", "cortex.qa.pi-admission.v1") || !oneOf(facts.ToolPolicy, "", "read,grep,find,ls") || (facts.RenderedInputSHA256 != "" && !hex64.MatchString(facts.RenderedInputSHA256)) || !oneOf(facts.Stop, "", "none", "launch_failed", "execution_timed_out", "execution_failed", "output_silent", "output_truncated", "normalization_failed", "observed_identity_mismatch", "fallback_observed", "policy_violation", "binding_stale") || !oneOf(facts.Usage, "", "unavailable") {
		return fmt.Errorf("invalid execution facts")
	}
	if facts.Completeness == "" && facts.Truncation == "" {
		return nil
	}
	if facts.Completeness == "complete" && facts.Truncation == "none" {
		return nil
	}
	if facts.Completeness == "truncated" && decimal(facts.Truncation) {
		return nil
	}
	return fmt.Errorf("invalid execution completeness")
}
func hasAdmittedEvidence(receipt Receipt) bool {
	return receipt.Availability == (AvailabilityFacts{Model: "available", Authentication: "ready", Fallback: "none"}) &&
		receipt.Execution.InvocationContract == "cortex.qa.pi-admission.v1" &&
		receipt.Execution.ToolPolicy == "read,grep,find,ls" &&
		hex64.MatchString(receipt.Execution.RenderedInputSHA256) &&
		receipt.Execution.Stop == "none" &&
		receipt.Execution.Usage == "unavailable" &&
		receipt.Execution.Completeness == "complete" &&
		receipt.Execution.Truncation == "none" &&
		receipt.Route.Observed.Provider == receipt.Route.Resolved.Provider &&
		receipt.Route.Observed.Model == receipt.Route.Resolved.Model
}

func validateDiagnostic(diagnostic BoundedDiagnostic, bounds BoundsFacts) error {
	observed, observedErr := strconv.Atoi(diagnostic.ObservedBytes)
	retained, retainedErr := strconv.Atoi(diagnostic.RetainedBytes)
	maximum := bounds.StdoutBytes
	if diagnostic.Source == "stderr" {
		maximum = bounds.StderrBytes
	}
	if (diagnostic.Source != "stdout" && diagnostic.Source != "stderr") || observedErr != nil || observed < 0 || observed > maximum || retainedErr != nil || retained < 0 || retained > bounds.DiagnosticBytes || !hex64.MatchString(diagnostic.RetainedSHA256) || !redaction.MatchString(diagnostic.Redaction) {
		return fmt.Errorf("invalid diagnostic metadata")
	}
	if diagnostic.Completeness == "complete" && diagnostic.Truncation == "none" {
		return nil
	}
	if diagnostic.Completeness != "truncated" || !decimal(diagnostic.Truncation) || diagnostic.Truncation != strconv.Itoa(retained) {
		return fmt.Errorf("invalid diagnostic truncation")
	}
	return nil
}

func decimal(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] == '0' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func mustAttempt(code Code) bool {
	switch code {
	case CodeLaunchFailed, CodeExecutionTimedOut, CodeExecutionFailed, CodeOutputSilent, CodeOutputTruncated, CodeObservedIdentityMismatch, CodeFallbackObserved:
		return true
	}
	return false
}
func mayAttempt(code Code) bool {
	return code == CodeAdmitted || mustAttempt(code) || code == CodeNonNaNRoute || code == CodeBindingStale || code == CodeNormalizationFailed || code == CodePolicyViolation
}
