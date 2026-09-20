// Package cli provides Cortex command orchestration without process ownership.
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

	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skillroot"
	"github.com/refactor-ia/cortex/internal/uninstalltxn"
)

const (
	exitOK          = 0
	exitUnknown     = 2
	exitConflict    = 3
	exitTransaction = 4
	exitUsage       = 64
	exitFailure     = 70
)

type uninstallDependencies struct {
	resolveRoots func() ([]skillroot.UninstallRoot, error)
	rootExists   func(string) (bool, error)
	observe      func(installobserve.UninstallRoot, installobserve.Options) (installobserve.UninstallObservation, error)
	applyGroup   func([]uninstalltxn.GroupRequest, string, string) error
	backupName   func() string
}

type uninstallPreflight struct {
	root        skillroot.UninstallRoot
	observation installobserve.UninstallObservation
	status      string
}

// Run executes one Cortex command with the supplied runtime probe seam.
// A nil runner selects the constrained production system probe.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner) int {
	return runWithDependencies(ctx, args, stdout, stderr, runner, defaultInstallDependencies(), defaultUninstallDependencies())
}

func runWithUninstallDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, uninstall uninstallDependencies) int {
	return runWithDependencies(ctx, args, stdout, stderr, runner, defaultInstallDependencies(), uninstall)
}

func runWithDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, install installDependencies, uninstall uninstallDependencies) int {
	if len(args) == 0 || len(args) != 1 && args[0] != "qa" && args[0] != "update" {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	switch args[0] {
	case "doctor":
		return runDoctor(ctx, stdout, stderr, runner, install.policy)
	case "qa":
		return runQA(ctx, args[1:], stdout, stderr)
	case "update":
		// Bare update keeps the all-runtime transaction; any argument selects
		// the explicit single-runtime update, which owns its own flag parsing.
		if len(args) > 1 {
			return runUpdate(ctx, args, stdout, stderr, runner)
		}
		return runInstall(ctx, stdout, stderr, runner, args[0], install)
	case "install":
		return runInstall(ctx, stdout, stderr, runner, args[0], install)
	case "uninstall":
		return runUninstall(stdout, stderr, uninstall)
	default:
		writeError(stderr, "invalid_command")
		return exitUsage
	}
}

func runDoctor(ctx context.Context, stdout, stderr io.Writer, runner runtimeprobe.Runner, policy runtimecompat.Policy) int {
	matrix, err := probeCompatibilityMatrix(ctx, runner, policy)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	if _, err := io.WriteString(stdout, runtimeReport(matrix)); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	// The exit code answers whether an install can proceed, not whether every
	// runtime is certified. An admitted uncertified version is disclosed in the
	// report and does not make the host uncertain.
	for _, decision := range matrix.Decisions {
		switch decision.Outcome {
		case runtimematrix.OutcomeAbsent, runtimematrix.OutcomePresentCompatible, runtimematrix.OutcomePresentUncertified:
		default:
			return exitUnknown
		}
	}
	return exitOK
}

const (
	qaUsage = "usage: cortex qa run --role <role> --request <file> --catalog <dir>\n"
	// qaReportNote surfaces the honest runtime prerequisites on every failure.
	// The default route provider is the policy placeholder "nan"; Cortex applies
	// no model fallback and owns no automatic configuration.
	// Still Pi-shaped on purpose: it stays a single-runtime note until the
	// per-backend availability report lands (T6 of the backend port).
	qaReportNote = "note=prerequisites: Pi 0.85.1 runtime and installed cortex assets; the default route provider is the policy placeholder \"nan\" and cortex applies no model fallback\n"
)

// qaPiResolver is the Pi binary resolution seam; nil selects the production
// constrained lookup.
var qaPiResolver qapi.PathResolver

// runQA executes one local QA report command. It never mutates user
// configuration and never claims admission.
func runQA(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) != 7 || args[0] != "run" || args[1] != "--role" || args[3] != "--request" || args[4] == "" || args[5] != "--catalog" || args[6] == "" {
		writeError(stderr, "invalid_arguments")
		_, _ = io.WriteString(stderr, qaUsage)
		return exitUsage
	}
	role := qarole.RoleID(args[2])
	if _, err := qarole.ValidateSquad([]qarole.RoleID{role}); err != nil {
		writeError(stderr, "invalid_arguments")
		_, _ = io.WriteString(stderr, qaUsage)
		return exitUsage
	}
	task, err := readRequestTask(args[4])
	if err != nil {
		return qaFailure(stderr, "invalid_request")
	}
	cwd, err := os.Getwd()
	catalogRoot, absErr := filepath.Abs(args[6])
	if err != nil || absErr != nil {
		return qaFailure(stderr, "invalid_request")
	}
	installRoot, err := piInstallRoot()
	if err != nil {
		return qaFailure(stderr, "actor_unavailable")
	}
	report, code, err := qapi.RunLocalReport(ctx, qapi.ReportRequest{
		Role: role, CatalogRoot: catalogRoot, InstallRoot: installRoot,
		CurrentDirectory: cwd, Task: task, TimeoutSeconds: qaadmission.DefaultTimeoutSeconds,
	}, qaPiResolver)
	if err != nil || code != "" {
		failure := qaFailure(stderr, string(code))
		if code == qaadmission.CodeNormalizationFailed {
			var diagnostic *qapi.ReportNormalizationError
			if errors.As(err, &diagnostic) {
				_, _ = fmt.Fprintf(stderr, "normalization_stage=%s normalization_reason=%s\n", diagnostic.Stage, diagnostic.Reason)
			}
		}
		return failure
	}
	if _, err := io.WriteString(stdout, report+"\n"); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return exitOK
}

func qaFailure(stderr io.Writer, code string) int {
	writeError(stderr, code)
	_, _ = io.WriteString(stderr, qaReportNote)
	return exitFailure
}

func readRequestTask(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > qaadmission.MaxTaskBytes {
		return nil, fmt.Errorf("request is not a bounded regular file")
	}
	return os.ReadFile(path)
}

// piInstallRoot resolves the installed asset root for Pi only. It stays
// Pi-shaped until the receipt contract carries backend identity (T5 of the
// backend port), which is what lets the caller ask for the root of whichever
// backend it resolved rather than assuming one.
func piInstallRoot() (string, error) {
	roots, err := skillroot.ResolveSystemUninstallRoots()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if root.RuntimeID() == runtimematrix.RuntimePi {
			return root.RootPath(), nil
		}
	}
	return "", fmt.Errorf("pi root unavailable")
}

func runUncertifiedOperation(ctx context.Context, stdout, stderr io.Writer, runner runtimeprobe.Runner, operation string) int {
	matrix, err := probeMatrix(ctx, runner)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	output := "operation=" + operation + " status=not_applied reason=compatibility_uncertified touch=denied\n" + runtimeReport(matrix)
	if _, err := io.WriteString(stdout, output); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return exitUnknown
}

func probeMatrix(ctx context.Context, runner runtimeprobe.Runner) (runtimematrix.Matrix, error) {
	reports, err := probe(ctx, runner)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	observations, err := runtimeprobe.Observations(reports)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	return runtimematrix.Decide(observations)
}

func probeCompatibilityMatrix(ctx context.Context, runner runtimeprobe.Runner, policy runtimecompat.Policy) (runtimematrix.Matrix, error) {
	reports, err := probe(ctx, runner)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	observations, err := policy.Evaluate(reports)
	if err != nil {
		return runtimematrix.Matrix{}, err
	}
	return runtimematrix.Decide(observations)
}

func runtimeReport(matrix runtimematrix.Matrix) string {
	var output strings.Builder
	for _, decision := range matrix.Decisions {
		output.WriteString(installRuntimeLine(decision.ID, decision.Outcome, decision.Action, false))
	}
	return output.String()
}

func defaultUninstallDependencies() uninstallDependencies {
	return uninstallDependencies{
		resolveRoots: skillroot.ResolveSystemUninstallRoots,
		rootExists:   uninstallRootExists,
		observe:      installobserve.ObserveUninstall,
		applyGroup:   uninstalltxn.ApplyGroup,
		backupName:   nextUninstallBackupName,
	}
}

func runUninstall(stdout, stderr io.Writer, deps uninstallDependencies) int {
	roots, err := deps.resolveRoots()
	if err != nil || len(roots) != 3 || roots[0].RuntimeID() != runtimematrix.RuntimePi ||
		roots[1].RuntimeID() != runtimematrix.RuntimeOpenCode || roots[2].RuntimeID() != runtimematrix.RuntimeClaudeCode {
		writeError(stderr, "uninstall_root_resolution_failed")
		return exitFailure
	}
	preflight := make([]uninstallPreflight, 0, len(roots))
	for _, root := range roots {
		exists, err := deps.rootExists(root.RootPath())
		if err != nil {
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		if !exists {
			preflight = append(preflight, uninstallPreflight{root: root, status: "not_installed"})
			continue
		}
		trusted, err := installobserve.NewUninstallRoot(root.RuntimeID(), root.RootKind(), root.RootPath())
		if err != nil {
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		observation, err := deps.observe(trusted, installobserve.DefaultOptions())
		if err != nil {
			writeError(stderr, "uninstall_observation_failed")
			return exitFailure
		}
		status := "ready"
		if len(observation.Records()) == 0 {
			status = "not_installed"
		} else if !observation.Ready() {
			status = "conflict"
		}
		preflight = append(preflight, uninstallPreflight{root: root, observation: observation, status: status})
	}
	for _, item := range preflight {
		if item.status == "conflict" {
			for index := range preflight {
				if preflight[index].status == "ready" {
					preflight[index].status = "blocked"
				}
			}
			return writeUninstallResult(stdout, stderr, preflight, exitConflict)
		}
	}
	requests := make([]uninstalltxn.GroupRequest, 0, len(preflight))
	for _, item := range preflight {
		if item.status == "ready" {
			requests = append(requests, uninstalltxn.GroupRequest{
				RuntimeID: item.root.RuntimeID(), Root: item.root.RootPath(), Observation: item.observation,
			})
		}
	}
	if len(requests) == 0 {
		return writeUninstallResult(stdout, stderr, preflight, exitOK)
	}
	backupRoot := filepath.Join(requests[0].Root, ".cortex")
	if err := deps.applyGroup(requests, backupRoot, deps.backupName()); err != nil {
		for index := range preflight {
			if preflight[index].status == "ready" {
				preflight[index].status = "failed"
			}
		}
		writeError(stderr, uninstallFailureReason(err))
		return writeUninstallResult(stdout, stderr, preflight, exitTransaction)
	}
	for index := range preflight {
		if preflight[index].status == "ready" {
			preflight[index].status = "completed"
		}
	}
	return writeUninstallResult(stdout, stderr, preflight, exitOK)
}

func nextUninstallBackupName() string {
	return "uninstall-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func uninstallRootExists(root string) (bool, error) {
	_, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// uninstallFailureReason classifies a group transaction failure as a stable
// machine-readable code. It never surfaces the underlying error text, which
// may carry private filesystem detail.
func uninstallFailureReason(err error) string {
	switch {
	case errors.Is(err, uninstalltxn.ErrConflict):
		return "ownership_conflict"
	case errors.Is(err, uninstalltxn.ErrInvalid):
		return "unsupported_installation"
	default:
		return "transaction_failed"
	}
}

func writeUninstallResult(stdout, stderr io.Writer, results []uninstallPreflight, code int) int {
	var output strings.Builder
	for _, result := range results {
		remove, absent, conflict := uninstallCounts(result.observation)
		_, _ = fmt.Fprintf(&output, "runtime=%s uninstall=%s remove=%d absent=%d conflict=%d\n", result.root.RuntimeID(), result.status, remove, absent, conflict)
	}
	if _, err := io.WriteString(stdout, output.String()); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return code
}

func uninstallCounts(observation installobserve.UninstallObservation) (remove, absent, conflict int) {
	for _, record := range observation.Records() {
		switch record.Status {
		case installobserve.UninstallRemove:
			remove++
		case installobserve.UninstallAbsent:
			absent++
		case installobserve.UninstallConflict:
			conflict++
		}
	}
	return
}

func probe(ctx context.Context, runner runtimeprobe.Runner) ([]runtimeprobe.Report, error) {
	if runner == nil {
		return runtimeprobe.ProbeSystem(ctx)
	}
	return runtimeprobe.ProbeAll(ctx, runner)
}

func writeError(stderr io.Writer, code string) {
	_, _ = io.WriteString(stderr, "error="+code+"\n")
}
