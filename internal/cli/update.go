package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/ownership"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// updateDependencies carries the update command's seams. The default
// compatibility evaluation is the production release-certification policy.
// An explicit local update may use a parsed uncertified version; fixture
// observations are never certification evidence.
type updateDependencies struct {
	compatibility func([]runtimeprobe.Report) ([]runtimematrix.Observation, error)
	resolveInputs func() (skillroot.Inputs, error)
}

func defaultUpdateDependencies() updateDependencies {
	return updateDependencies{compatibility: runtimecompat.BuiltInPolicy().Evaluate, resolveInputs: systemRootInputs}
}

// systemRootInputs reads only the system home and documented runtime overrides.
func systemRootInputs() (skillroot.Inputs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return skillroot.Inputs{}, err
	}
	return skillroot.Inputs{Home: home, PiCodingAgentDir: os.Getenv("PI_CODING_AGENT_DIR"), ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR")}, nil
}

// runUpdate executes one explicit single-runtime update command: the default
// mode is a read-only plan, --apply is required for any write, and every write
// stays inside the selected runtime's Cortex-owned root.
func runUpdate(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner) int {
	return runWithUpdateDependencies(ctx, args, stdout, stderr, runner, defaultUpdateDependencies())
}

const updateHelp = "usage: cortex update --runtime <pi|opencode|claude-code> --catalog <dir> [--apply] [--adopt-guidance]\n\n" +
	"Default mode is a read-only plan; --apply is required to write.\n" +
	"--adopt-guidance is Pi-only explicit adoption of verified legacy external guidance.\n" +
	"Registered guidance updates need no repeated adoption; drift is always refused.\n" +
	"Composed restart recovery is unsupported; the rollback command does not support it.\n" +
	"Composed uninstall remains fail-closed pending external-guidance preservation.\n"

func runWithUpdateDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, deps updateDependencies) int {
	if len(args) == 2 && args[0] == "update" && args[1] == "--help" {
		return writeHelp(stdout, stderr, updateHelp)
	}
	if len(args) == 0 || args[0] != "update" {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	runtimeID, catalogDir, apply, adoptGuidance, ok := parseUpdateArgs(args[1:])
	if !ok {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	reports, err := probe(ctx, runner)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	observations, err := deps.compatibility(reports)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	matrix, err := runtimematrix.DecideLocalUpdate(observations, runtimeID)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	var decision runtimematrix.Decision
	for _, candidate := range matrix.Decisions {
		if candidate.ID == runtimeID {
			decision = candidate
		}
	}
	if !decision.IncludeInTransaction {
		return writeUpdateRefusal(stdout, decision, updateRefusalReason(decision.Outcome))
	}
	plan, err := buildUpdateCandidate(runtimeID, catalogDir, observations, deps)
	if err != nil {
		writeError(stderr, "invalid_catalog")
		return exitFailure
	}
	if _, err := os.Lstat(plan.RootPath()); err != nil {
		return writeUpdateRefusal(stdout, decision, "runtime_root_absent")
	}
	observation, err := installobserve.Observe(plan, installobserve.DefaultOptions())
	if err != nil {
		writeError(stderr, "observation_failed")
		return exitFailure
	}
	warning := updateCertificationWarning(observations, runtimeID, decision)
	if updateNeedsActorGuidance(plan, observation) {
		return updateActorGuidance(stdout, stderr, plan, observation, observations, apply, adoptGuidance, warning)
	}
	classified, err := installcoord.Classify(plan, observation)
	if err != nil {
		writeError(stderr, "classification_failed")
		return exitFailure
	}
	if !apply {
		header := fmt.Sprintf("runtime=%s outcome=%s action=%s root=%s mode=plan%s", decision.ID, decision.Outcome, decision.Action, plan.RootPath(), warning)
		return writeUpdateOperations(stdout, header, classified, "planned")
	}
	return applyUpdate(stdout, stderr, plan, observation, classified, observations, warning)
}

// parseUpdateArgs accepts exactly one --runtime and one --catalog plus an
// optional --apply and Pi-only --adopt-guidance; there is no default runtime.
func parseUpdateArgs(args []string) (runtimematrix.RuntimeID, string, bool, bool, bool) {
	var runtimeID, catalogDir string
	apply, adoptGuidance := false, false
	for len(args) > 0 {
		flag := args[0]
		args = args[1:]
		switch flag {
		case "--runtime":
			if runtimeID != "" || len(args) == 0 {
				return "", "", false, false, false
			}
			runtimeID, args = args[0], args[1:]
		case "--catalog":
			if catalogDir != "" || len(args) == 0 {
				return "", "", false, false, false
			}
			catalogDir, args = args[0], args[1:]
		case "--apply":
			if apply {
				return "", "", false, false, false
			}
			apply = true
		case "--adopt-guidance":
			if adoptGuidance {
				return "", "", false, false, false
			}
			adoptGuidance = true
		default:
			return "", "", false, false, false
		}
	}
	switch runtimematrix.RuntimeID(runtimeID) {
	case runtimematrix.RuntimePi, runtimematrix.RuntimeOpenCode, runtimematrix.RuntimeClaudeCode:
	default:
		return "", "", false, false, false
	}
	if catalogDir == "" || (adoptGuidance && runtimematrix.RuntimeID(runtimeID) != runtimematrix.RuntimePi) {
		return "", "", false, false, false
	}
	return runtimematrix.RuntimeID(runtimeID), catalogDir, apply, adoptGuidance, true
}

// buildUpdateCandidate builds the validated bundle-bound candidate for the
// selected runtime from the explicitly supplied catalog. Pi keeps its
// disclosed actor-aware v2 representation through BuildActorAware; every other
// runtime keeps its supported skill-only plan without a Pi actor assumption.
func buildUpdateCandidate(runtimeID runtimematrix.RuntimeID, catalogDir string, observations []runtimematrix.Observation, deps updateDependencies) (installplan.Plan, error) {
	snapshot, err := catalog.BuildCatalogSnapshot(catalogDir, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		return installplan.Plan{}, err
	}
	inputs, err := deps.resolveInputs()
	if err != nil {
		return installplan.Plan{}, err
	}
	candidates, _, err := installcoord.BuildCandidates(installcoord.CandidateRequest{
		Snapshot:     snapshot,
		Observations: observations,
		LocalTarget:  runtimeID,
		// Admit is deliberately unset. This catalog comes from the operator on
		// the command line, not from the compiled release set, so the built-in
		// release admission does not apply to it.
		ResolveRoot: func(symbolic skilldest.Plan) (skillroot.Plan, error) {
			return skillroot.Resolve(symbolic, inputs)
		},
		Actors: bindPiActors,
		// An installation ID names an installation, not one command run, so the
		// identity already recorded at the resolved root is reused and a new one
		// is minted only when that root carries no valid actor-aware state.
		InstallationID: installcoord.ReuseInstallationID(installstate.DefaultInstallationIDGenerator()),
	})
	if err != nil {
		return installplan.Plan{}, err
	}
	for _, candidate := range candidates {
		if candidate.RuntimeID == runtimeID {
			return candidate.Plan, nil
		}
	}
	return installplan.Plan{}, fmt.Errorf("catalog projection has no artifacts for %s", runtimeID)
}

// bindPiActors binds the canonical Pi actor projection for one catalog snapshot.
func bindPiActors(snapshot catalog.CatalogSnapshot) (qaactor.Binding, error) {
	sources, err := qaactor.Sources(snapshot)
	if err != nil {
		return qaactor.Binding{}, err
	}
	set, err := qaactor.Render(sources)
	if err != nil {
		return qaactor.Binding{}, err
	}
	actorProjection, err := qaactor.ProjectPi(set)
	if err != nil {
		return qaactor.Binding{}, err
	}
	return qaactor.Bind(actorProjection)
}

// writeUpdateRefusal reports a denied update without planning or touching the
// filesystem, preserving the compatibility gate.
func writeUpdateRefusal(stdout io.Writer, decision runtimematrix.Decision, reason string) int {
	_, _ = fmt.Fprintf(stdout, "runtime=%s outcome=%s action=%s touch=denied status=not_applied reason=%s\n",
		decision.ID, decision.Outcome, decision.Action, reason)
	return exitUnknown
}

func updateCertificationWarning(observations []runtimematrix.Observation, target runtimematrix.RuntimeID, decision runtimematrix.Decision) string {
	if decision.Outcome != runtimematrix.OutcomePresentUncertified {
		return ""
	}
	for _, observation := range observations {
		if observation.ID == target {
			return fmt.Sprintf(" warning=uncertified_observed_version version=%s certification=not_certified", observation.Version)
		}
	}
	return ""
}

// updateRefusalReason maps a matrix outcome to its stable refusal reason.
func updateRefusalReason(outcome runtimematrix.Outcome) string {
	switch outcome {
	case runtimematrix.OutcomeKnownIncompatible:
		return "runtime_known_incompatible"
	case runtimematrix.OutcomeAbsent:
		return "runtime_absent"
	default:
		return "compatibility_uncertified"
	}
}

// writeUpdateOperations prints one read-only operation report with its status.
func writeUpdateOperations(stdout io.Writer, header string, classified installobserve.Result, status string) int {
	output := strings.Builder{}
	output.WriteString(header + "\n")
	for _, item := range classified.ArtifactDecisions() {
		if item.Action == ownership.Conflict {
			status = "conflict"
		}
		fmt.Fprintf(&output, "operation=%s action=%s\n", item.LogicalID, item.Action)
	}
	if classified.StateAction() == ownership.Conflict {
		status = "conflict"
	}
	fmt.Fprintf(&output, "operation=state/install-state action=%s\nstatus=%s\n", classified.StateAction(), status)
	_, _ = io.WriteString(stdout, output.String())
	if status == "conflict" {
		return exitConflict
	}
	return exitOK
}

// applyUpdate coordinates the explicit single-runtime transaction. Readiness is
// revalidated through installcoord.Preflight; the existing transaction
// primitives own backup capture, ordered writes, final readback, and rollback.
func applyUpdate(stdout, stderr io.Writer, plan installplan.Plan, observation installobserve.FilesystemObservation, classified installobserve.Result, observations []runtimematrix.Observation, warning string) int {
	// The header reports the mode the caller asked for. A refusal on this path
	// is still an apply attempt, and reporting it as a plan misstates what ran.
	header := fmt.Sprintf("runtime=%s root=%s mode=apply%s", plan.RuntimeID(), plan.RootPath(), warning)
	report, err := installcoord.PreflightLocalUpdate(observations, plan.RuntimeID(), []installcoord.Unit{{Plan: plan, Observation: observation}})
	if err != nil {
		writeError(stderr, "preflight_failed")
		return exitFailure
	}
	if !report.Ready() {
		return writeUpdateOperations(stdout, header, classified, "conflict")
	}
	backupRoot := filepath.Join(plan.RootPath(), ".cortex")
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		writeError(stderr, "backup_unavailable")
		return exitTransaction
	}
	backupName := nextUpdateBackupName()
	result, txErr := applyCandidate(plan, observation, backupRoot, backupName)
	if txErr != nil {
		if errors.Is(txErr, installtxn.ErrConflict) {
			return writeUpdateOperations(stdout, header, classified, "conflict")
		}
		writeError(stderr, "update_transaction_failed")
		return exitTransaction
	}
	output := strings.Builder{}
	fmt.Fprintf(&output, "runtime=%s root=%s mode=apply%s\n", plan.RuntimeID(), plan.RootPath(), warning)
	for _, action := range result.Actions() {
		fmt.Fprintf(&output, "operation=%s action=%s\n", action.LogicalID, action.Action)
	}
	fmt.Fprintf(&output, "backup=%s\n", backupName)
	if result.TransactionID().Valid() {
		fmt.Fprintf(&output, "transaction=%s\n", result.TransactionID())
	}
	fmt.Fprintf(&output, "status=applied\n")
	_, _ = io.WriteString(stdout, output.String())
	return exitOK
}

// applyCandidate selects the minimum existing transaction for the candidate
// representation: skill-only v1 plans use Apply; Pi actor-aware v2 plans use
// ApplyVerified with its fresh preflight and final readback.
func applyCandidate(plan installplan.Plan, observation installobserve.FilesystemObservation, backupRoot, backupName string) (installtxn.Result, error) {
	switch plan.InstalledState().SchemaVersion() {
	case 1:
		return installtxn.Apply(plan, observation, backupRoot, backupName)
	case 2:
	default:
		return installtxn.Result{}, installtxn.ErrInvalid
	}
	cwd, err := os.Getwd()
	if err != nil {
		return installtxn.Result{}, err
	}
	return installtxn.ApplyVerified(plan, cwd, backupRoot, backupName)
}

// Routing recognizes captured guidance, including malformed reserved markers,
// but never treats marker presence or readiness as ownership/write authority.
func updateNeedsActorGuidance(plan installplan.Plan, observation installobserve.FilesystemObservation) bool {
	if plan.RuntimeID() != runtimematrix.RuntimePi {
		return false
	}
	if prior := observation.PriorState(); prior != nil && prior.Manifest.SchemaVersion() == 3 {
		return true
	}
	for _, file := range plan.Files() {
		if file.Role() == "actor" {
			if exact, found := observation.Exact(file.LogicalID()); found {
				_, external, err := qaactor.ParseGuidance(exact.Bytes())
				if err != nil || len(external) != 0 {
					return true
				}
			}
		}
	}
	return false
}

func updateActorGuidance(stdout, stderr io.Writer, canonical installplan.Plan, observation installobserve.FilesystemObservation, observations []runtimematrix.Observation, apply, adopt bool, warning string) int {
	mode := "plan"
	if apply {
		mode = "apply"
	}
	header := fmt.Sprintf("runtime=%s root=%s mode=%s%s recovery=unsupported", canonical.RuntimeID(), canonical.RootPath(), mode, warning)
	cwd, err := os.Getwd()
	if err != nil {
		writeError(stderr, "observation_failed")
		return exitFailure
	}
	preflight, err := installcoord.PreflightActorGuidance(observations, installcoord.Unit{Plan: canonical, Observation: observation}, adopt, cwd)
	if err != nil || !preflight.Report().Ready() {
		_, _ = fmt.Fprintf(stdout, "%s\nstatus=conflict reason=actor_guidance_unverified\n", header)
		return exitConflict
	}
	proof := preflight.Proof() // Retain the exact immutable candidate and evidence.
	if !apply {
		return writeUpdateOperations(stdout, header, proof.Result(), "planned")
	}
	backupName := nextUpdateBackupName()
	result, err := installtxn.ApplyActorGuidance(canonical, observation, proof, cwd, filepath.Join(canonical.RootPath(), ".cortex"), backupName)
	if err != nil {
		writeError(stderr, "update_transaction_failed")
		return exitTransaction
	}
	output := strings.Builder{}
	output.WriteString(header + "\n")
	for _, action := range result.Actions() {
		fmt.Fprintf(&output, "operation=%s action=%s\n", action.LogicalID, action.Action)
	}
	if result.TransactionID().Valid() {
		fmt.Fprintf(&output, "backup=%s\ntransaction=%s\n", backupName, result.TransactionID())
	}
	output.WriteString("status=applied\n")
	_, _ = io.WriteString(stdout, output.String())
	return exitOK
}

func nextUpdateBackupName() string {
	return "install-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
