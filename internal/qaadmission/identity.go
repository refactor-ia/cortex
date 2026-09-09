package qaadmission

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/refactor-ia/cortex/internal/qaroute"
	"strconv"
)

const EffortUnobservable = "unobservable"

type ObservedEffort struct {
	Availability, Value string
}
type ObservedIdentity struct {
	Provider, Model string
	Effort          ObservedEffort
}
type RouteIdentity struct {
	Requested RequestedIdentity
	Resolved  qaroute.ResolvedRoute
	Observed  ObservedIdentity
}
type RequestedIdentity struct {
	Provider, Model, Effort string
}

func UnobservableEffort() ObservedEffort {
	return ObservedEffort{Availability: EffortUnobservable}
}
func ReceiptID(receipt Receipt) string {
	hash := sha256.New()
	for _, value := range receiptFields(receipt) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return "receipt." + fmt.Sprintf("%x", hash.Sum(nil))
}
func receiptFields(receipt Receipt) []string {
	diagnostic := BoundedDiagnostic{}
	if receipt.Diagnostic != nil {
		diagnostic = *receipt.Diagnostic
	}
	fields := []string{
		receipt.Contract, string(receipt.Status), string(receipt.Code), strconv.FormatBool(receipt.AttemptedRun), string(receipt.Role), receipt.Backend,
		receipt.Versions.Receipt, receipt.Versions.Policy, receipt.Versions.Profile, receipt.Versions.Adapter, receipt.Versions.ActorContract, receipt.Versions.SkillContract, receipt.Versions.InputContract, receipt.Versions.ProbeContract, receipt.Versions.Runtime,
		string(receipt.Installation.ID), receipt.Installation.CatalogSHA256, receipt.Installation.ActorSourceSHA256, receipt.Installation.ActorGeneratedSHA256, receipt.Installation.ActorBindingSHA256, receipt.Installation.SkillGeneratedSHA256,
		receipt.Target.CWDIdentity, receipt.Target.Revision, receipt.Target.Tree, receipt.Target.Fingerprint,
		receipt.Route.Requested.Provider, receipt.Route.Requested.Model, receipt.Route.Requested.Effort,
		receipt.Route.Resolved.PolicyVersion, string(receipt.Route.Resolved.Role), receipt.Route.Resolved.Backend, receipt.Route.Resolved.Provider, receipt.Route.Resolved.Model, receipt.Route.Resolved.Effort,
		receipt.Route.Observed.Provider, receipt.Route.Observed.Model, receipt.Route.Observed.Effort.Availability, receipt.Route.Observed.Effort.Value,
		receipt.Route.Resolved.ProfileID, receipt.Route.Resolved.ProfileSHA256, strconv.Itoa(len(receipt.Route.Resolved.OverrideFields)),
		receipt.Availability.Model, receipt.Availability.Authentication, receipt.Availability.Fallback,
		receipt.Execution.InvocationContract, receipt.Execution.ToolPolicy, receipt.Execution.RenderedInputSHA256, receipt.Execution.Stop, receipt.Execution.Usage, receipt.Execution.Completeness, receipt.Execution.Truncation,
		diagnostic.Source, diagnostic.Redaction, diagnostic.ObservedBytes, diagnostic.RetainedBytes, diagnostic.RetainedSHA256, diagnostic.Truncation, diagnostic.Completeness,
	}
	fields = append(fields, receipt.Route.Resolved.OverrideFields...)
	return append(fields, receipt.Bounds.fields()...)
}
