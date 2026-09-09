// Package cli provides Cortex command orchestration without process ownership.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/refactor-ia/cortex/internal/installobserve"
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
	if len(args) != 1 {
		writeError(stderr, "invalid_command")
		return exitUsage
	}
	switch args[0] {
	case "doctor":
		return runDoctor(ctx, stdout, stderr, runner)
	case "install", "update":
		return runInstall(ctx, stdout, stderr, runner, args[0], install)
	case "uninstall":
		return runUninstall(stdout, stderr, uninstall)
	default:
		writeError(stderr, "invalid_command")
		return exitUsage
	}
}

func runDoctor(ctx context.Context, stdout, stderr io.Writer, runner runtimeprobe.Runner) int {
	matrix, err := probeMatrix(ctx, runner)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	if _, err := io.WriteString(stdout, runtimeReport(matrix)); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	for _, decision := range matrix.Decisions {
		if decision.Outcome != runtimematrix.OutcomeAbsent {
			return exitUnknown
		}
	}
	return exitOK
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

func runtimeReport(matrix runtimematrix.Matrix) string {
	var output strings.Builder
	for _, decision := range matrix.Decisions {
		output.WriteString("runtime=")
		output.WriteString(string(decision.ID))
		if decision.Outcome == runtimematrix.OutcomeAbsent {
			output.WriteString(" presence=absent")
		} else {
			output.WriteString(" presence=present compatibility=unknown")
		}
		output.WriteString(" action=")
		output.WriteString(string(decision.Action))
		output.WriteString(" touch=denied\n")
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
