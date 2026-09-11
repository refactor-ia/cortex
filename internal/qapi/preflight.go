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
