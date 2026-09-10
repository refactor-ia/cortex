package qapi

import (
	"context"
	"errors"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installobserve"
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

func identityInsufficient(stage string, cause error) error {
	return &IdentityInsufficientError{Stage: stage, cause: cause}
}
