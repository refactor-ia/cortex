package qapi

import (
	"context"

	"github.com/refactor-ia/cortex/internal/qaroute"
)

// Backend is the execution port one QA report run needs from a runtime. It
// carries exactly the four steps the local report pipeline sequences — bind the
// runtime, build the invocation, encode the role input, normalize the terminal
// message — plus the backend identity that route resolution, catalog admission,
// and the receipt all key on.
//
// The port deliberately excludes process execution and its bounds. runOnce,
// minimalEnvironment, cappedWriter, and the timeout budget stay shared and
// unexported so no adapter can widen the isolation guarantees the QA vertical
// depends on: one child process, a minimal environment, a single tool-free
// turn. Only Invocation — a binary, a working directory, and an argv — crosses
// the boundary, which keeps "what to run" backend-specific and "how it runs"
// uniform.
//
// Availability and qualification probes are also excluded. They answer a
// different question than running a report, they are Pi-shaped today, and
// RunLocalReport produces no receipt, so nothing in this path needs them.
type Backend interface {
	// ID is the stable backend token recorded in the route and the catalog
	// admission binding. It is policy identity, not a display name.
	ID() string
	// Bind resolves and verifies the runtime once for one run, in cwd.
	Bind(ctx context.Context, cwd string) (BoundRuntime, error)
	// BuildInvocation constructs no process, shell, filesystem, or provider
	// call; it only assembles the exact token contract for this backend.
	BuildInvocation(route qaroute.ResolvedRoute, paths BoundInvocationPaths) (Invocation, error)
	// EncodeInput frames one bounded report input for this backend.
	EncodeInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, task []byte) ([]byte, error)
	// ParseReport extracts one substantive report from this backend's stdout.
	// A nil error is the only signal of success; on rejection it reports which
	// fixed parser condition failed and never echoes stream content.
	ParseReport(stdout []byte) (string, *ReportNormalizationError)
}

// BoundRuntime is the part of a verified runtime binding that crosses the
// backend port. A binding carries far more than this — Pi retains file
// identity, mode, size, digest, and the version capture so it can revalidate
// that the executable did not change under it — but the report pipeline only
// needs the two values it turns into an invocation. Keeping the port this
// narrow means an adapter cannot be tempted to leak its own binding details
// into the shared sequence.
type BoundRuntime interface {
	// Path is the canonical absolute path of the bound executable.
	Path() string
	// CWD is the canonical absolute working directory of the run.
	CWD() string
}

// PathResolver resolves one runtime candidate for a single binding attempt.
// It is the injection seam that lets a test bind a fixture executable without
// reaching for PATH, and it stays separate from the backend so a caller can
// choose the candidate without reimplementing the verification around it.
type PathResolver interface {
	Resolve(context.Context) (string, error)
}

// piBackendID is the policy token for the Pi backend. It must match the
// backend the route policy admits; qaroute owns that set.
const piBackendID = "pi"

// piBackend re-expresses the existing Pi path as one Backend. Every method
// delegates to the package functions that already implement it, so the Pi
// behavior, its argv, and its stream checks are the same ones as before the
// port existed.
type piBackend struct {
	resolver PathResolver
}

// NewPiBackend registers Pi as a QA backend. A nil resolver selects the
// production constrained PATH lookup, which is the only ambient state this
// package consults and only when the caller supplied no candidate.
func NewPiBackend(resolver PathResolver) Backend {
	if resolver == nil {
		resolver = lookPathPi{}
	}
	return piBackend{resolver: resolver}
}

func (piBackend) ID() string {
	return piBackendID
}

func (backend piBackend) Bind(ctx context.Context, cwd string) (BoundRuntime, error) {
	bound, err := bindPi(ctx, cwd, backend.resolver)
	if err != nil {
		return nil, err
	}
	return bound, nil
}

func (piBackend) BuildInvocation(route qaroute.ResolvedRoute, paths BoundInvocationPaths) (Invocation, error) {
	return BuildInvocation(route, paths)
}

func (piBackend) EncodeInput(route qaroute.ResolvedRoute, actorSHA256, skillSHA256 string, task []byte) ([]byte, error) {
	return EncodeReportInput(route, actorSHA256, skillSHA256, task)
}

func (piBackend) ParseReport(stdout []byte) (string, *ReportNormalizationError) {
	report, ok, diagnostic := parseReportStream(stdout)
	if !ok {
		return "", diagnostic
	}
	return report, nil
}
