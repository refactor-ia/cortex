package qapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qagit"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
)

// IdentityInsufficientError reports a preflight stage that could not establish
// mandatory admission identity. Its text is deliberately non-diagnostic.
type IdentityInsufficientError struct {
	Stage string
	cause error
}

func (err *IdentityInsufficientError) Error() string {
	return "admission identity is insufficient"
}

func (err *IdentityInsufficientError) Unwrap() error {
	return err.cause
}

type preflight struct {
	route  qaroute.ResolvedRoute
	assets installobserve.AdmissionAssets
	git    qagit.Binding
}

func preflightBinding(ctx context.Context, request AdmissionRequest, profileRoot, installRoot string, snapshot catalog.CatalogSnapshot, gitRunner qagit.Runner) (preflight, error) {
	if !validAdmissionRequest(request) {
		return preflight{}, errInvalidAdmissionRequest
	}

	expected, err := CatalogAdmissionBinding(snapshot, request.Role, request.Backend)
	if err != nil {
		return preflight{}, identityInsufficient("assets", err)
	}
	assets, err := installobserve.ObserveAdmissionAssets(installRoot, request.CurrentDirectory, expected)
	if err != nil {
		return preflight{}, identityInsufficient("assets", err)
	}
	route, failure := resolveProfileRoute(profileRoot, request)
	if failure.Code != "" {
		return preflight{}, identityInsufficient("route", errors.New(failure.Code))
	}
	binding, err := qagit.VerifyCleanBinding(ctx, qagit.Request{
		CWD: request.CurrentDirectory, Revision: request.Revision, Fingerprint: request.Fingerprint,
	}, gitRunner)
	if err != nil {
		return preflight{}, identityInsufficient("clean_binding", err)
	}
	return preflight{route: route, assets: assets, git: binding}, nil
}

// prelaunchOps contains the private boundary seams needed to test prelaunch composition.
type prelaunchOps struct {
	revalidatePi          func(boundPi) error
	catalogBinding        func(catalog.CatalogSnapshot, qarole.RoleID, string) (installobserve.AdmissionBinding, error)
	observeAssets         func(string, string, installobserve.AdmissionBinding) (installobserve.AdmissionAssets, error)
	verifyCleanBinding    func(context.Context, qagit.Request, qagit.Runner) (qagit.Binding, error)
	prelaunchReceiptBasis func(AdmissionRequest, preflight, boundPi) (qaadmission.Receipt, qaadmission.Code, error)
	probeAvailability     func(context.Context, boundPi, string, string) availabilityProbes
}

func defaultPrelaunchOps() prelaunchOps {
	return prelaunchOps{
		revalidatePi: revalidatePi, catalogBinding: CatalogAdmissionBinding,
		observeAssets: installobserve.ObserveAdmissionAssets, verifyCleanBinding: qagit.VerifyCleanBinding,
		prelaunchReceiptBasis: prelaunchReceiptBasis, probeAvailability: probeAvailability,
	}
}

// prelaunchAvailability rechecks one frozen flight before it runs availability probes.
// It returns a complete but unterminated receipt basis and a private preparation code.
func prelaunchAvailability(ctx context.Context, request AdmissionRequest, flight preflight, pi boundPi, installRoot string, snapshot catalog.CatalogSnapshot, gitRunner qagit.Runner, ops *prelaunchOps) (qaadmission.Receipt, qaadmission.Code, error) {
	if !validAdmissionRequest(request) {
		return qaadmission.Receipt{}, "", identityInsufficient("request", errInvalidAdmissionRequest)
	}
	if !validFrozenPreflight(request, flight) {
		return qaadmission.Receipt{}, "", identityInsufficient("preflight", errors.New("inconsistent frozen preflight"))
	}
	if !validPrelaunchOps(ops) {
		return qaadmission.Receipt{}, "", identityInsufficient("operations", errors.New("prelaunch operations are unavailable"))
	}
	canonicalCWD, err := canonicalRuntimeDirectory(request.CurrentDirectory)
	if err != nil {
		return qaadmission.Receipt{}, "", identityInsufficient("pi", err)
	}
	if pi.cwd != canonicalCWD {
		return qaadmission.Receipt{}, "", identityInsufficient("pi", errors.New("Pi working directory changed"))
	}
	if err := ops.revalidatePi(pi); err != nil {
		return qaadmission.Receipt{}, "", identityInsufficient("pi", err)
	}

	expected, err := ops.catalogBinding(snapshot, request.Role, request.Backend)
	if err != nil {
		return qaadmission.Receipt{}, "", identityInsufficient("assets", err)
	}
	assets, err := ops.observeAssets(installRoot, request.CurrentDirectory, expected)
	if err != nil || !samePreflightAssets(flight.assets, assets) {
		if err == nil {
			err = errors.New("admission assets changed")
		}
		return qaadmission.Receipt{}, "", identityInsufficient("assets", err)
	}
	binding, err := ops.verifyCleanBinding(ctx, qagit.Request{
		CWD: request.CurrentDirectory, Revision: request.Revision, Fingerprint: request.Fingerprint,
	}, gitRunner)
	if err != nil || !samePreflightGitBinding(flight.git, binding) {
		if err == nil {
			err = errors.New("clean binding changed")
		}
		return qaadmission.Receipt{}, "", identityInsufficient("clean_binding", err)
	}

	prepared := preflight{route: flight.route, assets: assets, git: binding}
	basis, code, err := ops.prelaunchReceiptBasis(request, prepared, pi)
	if err != nil {
		return qaadmission.Receipt{}, "", err
	}
	if code != qaadmission.CodeAdmitted {
		return basis, code, nil
	}
	availability := ops.probeAvailability(ctx, pi, flight.route.Provider, flight.route.Model)
	if !availability.model.Available {
		return basis, availability.model.Code, nil
	}
	if !availability.auth.Ready {
		return basis, availability.auth.Code, nil
	}
	return basis, qaadmission.CodeAdmitted, nil
}

func validFrozenPreflight(request AdmissionRequest, flight preflight) bool {
	return flight.assets.RoleID() == request.Role && flight.assets.Backend() == request.Backend &&
		flight.route.Role == request.Role && flight.route.Backend == request.Backend &&
		flight.git.Revision == request.Revision && flight.git.Fingerprint == request.Fingerprint
}

func validPrelaunchOps(ops *prelaunchOps) bool {
	return ops != nil && ops.revalidatePi != nil && ops.catalogBinding != nil && ops.observeAssets != nil &&
		ops.verifyCleanBinding != nil && ops.prelaunchReceiptBasis != nil && ops.probeAvailability != nil
}

func samePreflightAssets(original, refreshed installobserve.AdmissionAssets) bool {
	return original.InstallationID() == refreshed.InstallationID() &&
		original.CatalogFingerprint() == refreshed.CatalogFingerprint() && original.RoleID() == refreshed.RoleID() && original.Backend() == refreshed.Backend() &&
		original.ActorSHA256() == refreshed.ActorSHA256() && original.ActorSourceSHA256() == refreshed.ActorSourceSHA256() &&
		original.ActorBindingSHA256() == refreshed.ActorBindingSHA256() && original.SkillSHA256() == refreshed.SkillSHA256() &&
		original.ActorPath() == refreshed.ActorPath() && original.SkillPath() == refreshed.SkillPath()
}

func samePreflightGitBinding(original, refreshed qagit.Binding) bool {
	return original.CWDIdentity == refreshed.CWDIdentity && original.Revision == refreshed.Revision && original.Tree == refreshed.Tree &&
		original.Fingerprint == refreshed.Fingerprint && original.ObjectFormat != "" && original.ObjectFormat == refreshed.ObjectFormat
}

// prelaunchReceiptBasis prepares only the identity facts needed to size a future
// terminal receipt. The future adapter must revalidate all mutable facts before
// it probes or executes Pi.
func prelaunchReceiptBasis(request AdmissionRequest, flight preflight, pi boundPi) (qaadmission.Receipt, qaadmission.Code, error) {
	if !validAdmissionRequest(request) {
		return qaadmission.Receipt{}, "", identityInsufficient("request", errInvalidAdmissionRequest)
	}
	if flight.assets.RoleID() != request.Role || flight.assets.Backend() != request.Backend || flight.route.Role != request.Role || flight.route.Backend != request.Backend || flight.git.Revision != request.Revision || flight.git.Fingerprint != request.Fingerprint {
		return qaadmission.Receipt{}, "", identityInsufficient("preflight", errors.New("inconsistent preflight binding"))
	}
	if pi.version != RuntimeVersion || pi.size <= 0 || zeroDigest(pi.digest) {
		return qaadmission.Receipt{}, "", identityInsufficient("binary", errors.New("incomplete Pi binary identity"))
	}
	bounds, ok := qaadmission.BoundsForTimeout(request.TimeoutSeconds)
	if !ok {
		return qaadmission.Receipt{}, "", identityInsufficient("bounds", errors.New("invalid timeout bounds"))
	}

	basis := qaadmission.Receipt{
		Contract: qaadmission.Contract,
		Role:     request.Role,
		Backend:  request.Backend,
		Versions: qaadmission.Versions{
			Receipt: qaadmission.Contract, Policy: flight.route.PolicyVersion, Profile: qaroute.ProfileContract,
			Adapter: "cortex.qa.pi-admission.v1", ActorContract: qaactor.ActorContractVersion,
			SkillContract: skillContract, InputContract: InputContract, ProbeContract: ProbeContract, Runtime: pi.version,
		},
		Installation: qaadmission.InstallationIdentity{
			ID: flight.assets.InstallationID(), CatalogSHA256: flight.assets.CatalogFingerprint(),
			ActorSourceSHA256: flight.assets.ActorSourceSHA256(), ActorGeneratedSHA256: flight.assets.ActorSHA256(),
			ActorBindingSHA256: flight.assets.ActorBindingSHA256(), SkillGeneratedSHA256: flight.assets.SkillSHA256(),
		},
		Target: qaadmission.TargetIdentity{
			CWDIdentity: flight.git.CWDIdentity, Revision: flight.git.Revision, Tree: flight.git.Tree, Fingerprint: flight.git.Fingerprint,
		},
		Binary: qaadmission.BinaryIdentity{Contract: qaadmission.BinaryContract, SHA256: fmt.Sprintf("%x", pi.digest), SizeBytes: pi.size},
		Route: qaadmission.RouteIdentity{
			Requested: qaadmission.RequestedIdentity{Provider: request.Override.Provider, Model: request.Override.Model, Effort: request.Override.Effort},
			Resolved:  flight.route,
			Observed:  qaadmission.ObservedIdentity{Effort: qaadmission.UnobservableEffort()},
		},
		Bounds: bounds,
	}
	size, err := qaadmission.MinimumSize(basis)
	if err != nil {
		return qaadmission.Receipt{}, "", identityInsufficient("basis", err)
	}
	return basis, qaadmission.PrelaunchCode(basis, size), nil
}

func zeroDigest(digest [32]byte) bool {
	return digest == [32]byte{}
}

func identityInsufficient(stage string, cause error) error {
	return &IdentityInsufficientError{Stage: stage, cause: cause}
}
