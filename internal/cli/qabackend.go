package cli

import (
	"context"
	"os"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/qaroute"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// qaRuntimeBackends maps each supported runtime to the QA backend that runs on
// it. The two identifiers differ on purpose and the mapping is explicit rather
// than a string conversion: runtimematrix names the installation target
// ("claude-code"), qaroute names the execution policy token ("claude"), and
// nothing guarantees the two vocabularies stay spelled alike.
var qaRuntimeBackends = map[runtimematrix.RuntimeID]string{
	runtimematrix.RuntimePi:         "pi",
	runtimematrix.RuntimeOpenCode:   "opencode",
	runtimematrix.RuntimeClaudeCode: "claude",
}

// qaProbeRole is the role whose resolved route doctor probes with.
//
// It has to be one named role and not "the backend in general", because the
// question is route-dependent: the six roles resolve to three different models
// and at least one backend's availability probe asks whether the install can
// reach the resolved route's model. Probing all six would multiply doctor's
// subprocess budget by six to answer a question no maintainer asked.
//
// So doctor probes one role and prints which one. A report for another role
// may still find its own model unavailable; the emitted qa_probe_role field is
// what stops the line from being read as a claim about all six.
const qaProbeRole = qarole.RequirementsAnalyst

// QA availability states doctor emits. Every state other than the two below is
// one of the typed admission codes the receipt contract already uses, so a
// consumer reads one vocabulary instead of two.
const (
	// qaAvailabilityReady means the backend's own probes answered that it can
	// run a report for the probed route.
	qaAvailabilityReady = "ready"
	// qaAvailabilityNotProbed means the runtime is not present, so there was
	// nothing to probe. It is not a claim that the backend is unavailable —
	// it is the absence of a claim, which is the honest report when no
	// executable exists to ask.
	qaAvailabilityNotProbed = "not_probed"
)

// qaBackendIdentity reports how strongly one backend's version binding
// identifies the product it bound.
//
// This is not decoration. `pi --version` prints "pi 0.85.1" and
// `claude --version` prints "2.1.278 (Claude Code)", so both bindings reject a
// binary whose version line names another product. `opencode --version` prints
// a bare "1.18.25" with no product name, so its binding cannot tell OpenCode
// apart from any other CLI on PATH that prints a bare semantic version. The
// binding has that much certainty and no more, and doctor says so rather than
// presenting all three detections as equally identified.
func qaBackendIdentity(backend string) string {
	if backend == "opencode" {
		return "version_only"
	}
	return "named"
}

// qaBackendProbe is the backend availability seam. It is a variable so a test
// can answer for a fixture runtime without launching a real one; nil selects
// the production probe.
var qaBackendProbe func(ctx context.Context, backend string) string

// probeQABackend answers whether one backend could run a report, without
// running one. It binds the runtime and issues that backend's own fixed,
// bounded availability probes — the read-only prefix of a report run, and
// nothing past it.
func probeQABackend(ctx context.Context, backend string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return string(qaadmission.CodeUnsupportedRuntime)
	}
	verdict := qapi.ProbeBackendAvailability(ctx, backend, qaProbeRole, cwd, nil)
	if verdict.Available {
		return qaAvailabilityReady
	}
	return string(verdict.Code)
}

// qaBackendLine renders the QA backend fields doctor appends to one runtime
// line. present decides whether anything is probed at all, which is what keeps
// doctor's subprocess budget bounded by the runtimes that actually exist.
//
// The fields are emitted uniformly, including for an absent runtime, so the
// line stays parseable without per-case conditionals. Presence and
// availability are two fields and never one word, because a runtime can be
// installed, detected and version-bound while its backend has no usable
// credentials, and collapsing the two would report the second as settled by
// the first.
func qaBackendLine(ctx context.Context, id runtimematrix.RuntimeID, present bool) string {
	backend, known := qaRuntimeBackends[id]
	if !known {
		return ""
	}
	availability := qaAvailabilityNotProbed
	if present {
		probe := qaBackendProbe
		if probe == nil {
			probe = probeQABackend
		}
		availability = probe(ctx, backend)
	}
	return " qa_backend=" + backend + " qa_identity=" + qaBackendIdentity(backend) +
		" qa_probe_role=" + string(qaProbeRole) + " qa_availability=" + availability
}

// qaInstallRoot resolves the installed asset root of one QA backend. It
// replaces the Pi-only lookup the report path carried while only one backend
// could execute: the caller now chooses a backend, so the root it needs is the
// root of that backend's runtime and not of a fixed one.
func qaInstallRoot(backend string) (string, error) {
	runtimeID, known := qaroute.RuntimeFor(backend)
	if !known {
		return "", errUnknownQABackend
	}
	roots, err := skillroot.ResolveSystemUninstallRoots()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if root.RuntimeID() == runtimeID {
			return root.RootPath(), nil
		}
	}
	return "", errUnknownQABackend
}
