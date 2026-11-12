package cli

import (
	"context"
	"errors"
	"os"

	"github.com/refactor-ia/cortex/internal/builtinassets"
	"github.com/refactor-ia/cortex/internal/installobserve"
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
// the production probe. It answers with the whole verdict and not its code
// alone: a refusal carries the routed pair it was about (#339, #374 S7), and
// discarding that here is what left doctor unable to name it.
var qaBackendProbe func(ctx context.Context, backend string) qapi.AvailabilityVerdict

// qaAssetCheck is the installed-asset seam for the doctor QA line. It runs the
// same read-only admission check qa run performs: the binary's embedded catalog
// against the installed assets of the backend's runtime root (issue #294).
var qaAssetCheck func(backend string) error

// checkQAAssets verifies the installed assets of one backend against the
// binary's embedded catalog. Read-only: it observes, never writes.
func checkQAAssets(backend string) error {
	snapshot, err := builtinassets.Snapshot()
	if err != nil {
		return err
	}
	expected, err := qapi.CatalogAdmissionBinding(snapshot, qaProbeRole, backend)
	if err != nil {
		return err
	}
	installRoot, err := qaInstallRoot(backend)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, err = installobserve.ObserveAdmissionAssets(installRoot, cwd, expected)
	return err
}

// qaAssetAvailabilityFromError maps an admission failure onto the doctor QA
// availability token and its bounded diagnostic. Missing assets and mismatched
// assets are two tokens; the diagnostic carries only stages and hex digests.
//
// A refusal whose stage says the check itself could not conclude — the binding
// is not one this binary admits, a root is not canonical, the actor's guidance
// or shadow status could not be proven — gets its own token. Reporting those as
// assets_mismatch implied a digest disagreement that was never observed (#374
// S7); the stage name is the only detail that claim can carry.
//
// Any other check failure fails closed (issue #361): a failed check never
// degrades to ready.
func qaAssetAvailabilityFromError(err error) (string, string) {
	var unavailable *installobserve.AdmissionUnavailableError
	if !errors.As(err, &unavailable) {
		return "assets_check_failed", " qa_assets_stage=check_failed"
	}
	switch unavailable.Stage {
	case "install_state_missing", "install_state_unreadable", "skill_missing", "skill_unlisted", "actor_missing", "actor_unlisted":
		diagnostic := " qa_assets_stage=" + unavailable.Stage
		if unavailable.Expected != "" {
			diagnostic += " expected=" + unavailable.Expected
		}
		return "assets_missing", diagnostic
	case "binding_invalid", "install_root_invalid", "current_directory_invalid", "actor_guidance_invalid", "actor_shadow_unverified":
		return "assets_unverifiable", qaAssetStageDiagnostic(unavailable)
	}
	return "assets_mismatch", qaAssetStageDiagnostic(unavailable)
}

// qaAssetStageDiagnostic renders the stage and whatever identity the refusal
// carried. Expected and Found are hex digests or the catalog fingerprint, so
// the foreign-catalog case keeps naming both.
func qaAssetStageDiagnostic(unavailable *installobserve.AdmissionUnavailableError) string {
	diagnostic := " qa_assets_stage=" + unavailable.Stage
	if unavailable.Expected != "" {
		diagnostic += " expected=" + unavailable.Expected
	}
	if unavailable.Found != "" {
		diagnostic += " found=" + unavailable.Found
	}
	return diagnostic
}

// probeQABackend answers whether one backend could run a report, without
// running one. It binds the runtime and issues that backend's own fixed,
// bounded availability probes — the read-only prefix of a report run, and
// nothing past it.
func probeQABackend(ctx context.Context, backend string) qapi.AvailabilityVerdict {
	cwd, err := os.Getwd()
	if err != nil {
		return qapi.AvailabilityVerdict{Code: qaadmission.CodeUnsupportedRuntime}
	}
	return qapi.ProbeBackendAvailability(ctx, backend, qaProbeRole, cwd, nil)
}

// qaAvailabilityToken renders one verdict as the doctor availability token:
// ready, or the fixed admission code the probe reached. One vocabulary, not two.
func qaAvailabilityToken(verdict qapi.AvailabilityVerdict) string {
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
	diagnostic := ""
	if present {
		probe := qaBackendProbe
		if probe == nil {
			probe = probeQABackend
		}
		verdict := probe(ctx, backend)
		availability = qaAvailabilityToken(verdict)
		if verdict.Available {
			check := qaAssetCheck
			if check == nil {
				check = checkQAAssets
			}
			if err := check(backend); err != nil {
				if token, note := qaAssetAvailabilityFromError(err); token != "" {
					availability, diagnostic = token, note
				}
			}
		}
		if diagnostic == "" {
			diagnostic = qaRouteIdentity(verdict)
		}
	}
	return " qa_backend=" + backend + " qa_identity=" + qaBackendIdentity(backend) +
		" qa_probe_role=" + string(qaProbeRole) + " qa_availability=" + availability + diagnostic
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

// qaRouteIdentity renders the routed pair one availability verdict answered
// about, so a doctor line names what it probed instead of discarding it.
func qaRouteIdentity(verdict qapi.AvailabilityVerdict) string {
	if verdict.Available || verdict.Provider == "" || verdict.Model == "" {
		return ""
	}
	return " qa_route_provider=" + sanitizeRouteIdentity(verdict.Provider) + " qa_route_model=" + sanitizeRouteIdentity(verdict.Model)
}
