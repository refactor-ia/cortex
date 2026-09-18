package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/refactor-ia/cortex/internal/builtinassets"
	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installcoord"
	"github.com/refactor-ia/cortex/internal/installobserve"
	"github.com/refactor-ia/cortex/internal/installplan"
	"github.com/refactor-ia/cortex/internal/installtxn"
	"github.com/refactor-ia/cortex/internal/projection"
	"github.com/refactor-ia/cortex/internal/releasecatalog"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

type installDependencies struct {
	policy        runtimecompat.Policy
	home          func() (string, error)
	resolveRoot   func(skilldest.Plan) (skillroot.Plan, error)
	observe       func(installplan.Plan, installobserve.Options) (installobserve.FilesystemObservation, error)
	applyGroup    func([]installtxn.GroupRequest, string, string) (installtxn.GroupResult, error)
	backupName    func() (string, error)
	buildRequests func([]runtimematrix.Observation, installDependencies) ([]installtxn.GroupRequest, []projection.RuntimeResult, error)
}

func defaultInstallDependencies() installDependencies {
	return installDependencies{
		policy:        runtimecompat.BuiltInPolicy(),
		home:          os.UserHomeDir,
		resolveRoot:   skillroot.ResolveSystem,
		observe:       installobserve.Observe,
		applyGroup:    installtxn.ApplyGroup,
		backupName:    nextInstallBackupName,
		buildRequests: buildInstallRequests,
	}
}

func runWithInstallDependencies(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner, deps installDependencies) int {
	return runWithDependencies(ctx, args, stdout, stderr, runner, deps, defaultUninstallDependencies())
}

func runInstall(ctx context.Context, stdout, stderr io.Writer, runner runtimeprobe.Runner, operation string, deps installDependencies) int {
	reports, err := probe(ctx, runner)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	observations, err := deps.policy.Evaluate(reports)
	if err != nil {
		writeError(stderr, "install_composition_failed")
		return exitFailure
	}
	matrix, err := runtimematrix.Decide(observations)
	if err != nil {
		writeError(stderr, "install_composition_failed")
		return exitFailure
	}
	if !matrix.HasCompatible {
		status := "status=not_applied reason=compatibility_uncertified touch=denied"
		return writeInstallResult(stdout, stderr, operation, status, "", installResults(matrix.Decisions), false, installtxn.Counts{}, exitUnknown)
	}
	warning := uncertifiedAdmissionWarning(matrix)
	requests, results, err := deps.buildRequests(observations, deps)
	if err != nil {
		writeError(stderr, "install_composition_failed")
		return exitFailure
	}
	if len(requests) == 0 {
		return writeInstallResult(stdout, stderr, operation, "status=not_applied reason=projection_unrepresentable touch=denied", "", results, false, installtxn.Counts{}, exitUnknown)
	}
	home, err := deps.home()
	if err != nil || !existingDirectory(home) {
		writeError(stderr, "install_composition_failed")
		return exitFailure
	}
	backupName, err := deps.backupName()
	if err != nil || !validInstallBackupName(backupName) {
		writeError(stderr, "install_composition_failed")
		return exitFailure
	}
	group, err := deps.applyGroup(requests, home, backupName)
	if err != nil {
		reason, code := "transaction_failed", exitTransaction
		if errors.Is(err, installtxn.ErrConflict) {
			reason, code = "ownership_conflict", exitConflict
		}
		return writeInstallResult(stdout, stderr, operation, "status=not_applied reason="+reason+" touch=denied", "", results, false, installtxn.Counts{}, code)
	}
	return writeInstallResult(stdout, stderr, operation, "status=completed touch=applied", warning, results, true, group.Counts(), exitOK)
}

// uncertifiedAdmissionWarning discloses how many uncertified runtimes the
// transaction admitted without ever reporting an observed version.
func uncertifiedAdmissionWarning(matrix runtimematrix.Matrix) string {
	admitted := 0
	for _, decision := range matrix.Decisions {
		if decision.Outcome == runtimematrix.OutcomePresentUncertified && decision.IncludeInTransaction {
			admitted++
		}
	}
	if admitted == 0 {
		return ""
	}
	return " warning=uncertified_admission runtimes=" + strconv.Itoa(admitted) + " certification=not_certified"
}

func installResults(decisions []runtimematrix.Decision) []projection.RuntimeResult {
	results := make([]projection.RuntimeResult, len(decisions))
	for index, decision := range decisions {
		results[index] = projection.RuntimeResult{ID: decision.ID, Outcome: decision.Outcome, Action: decision.Action, IncludeInTransaction: decision.IncludeInTransaction, TouchAllowed: decision.TouchAllowed}
	}
	return results
}

func buildInstallRequests(observations []runtimematrix.Observation, deps installDependencies) ([]installtxn.GroupRequest, []projection.RuntimeResult, error) {
	snapshot, err := builtinassets.Snapshot()
	if err != nil {
		return nil, nil, err
	}
	candidates, final, err := installcoord.BuildCandidates(installcoord.CandidateRequest{
		Snapshot:     snapshot,
		Observations: observations,
		// The embedded catalog must be one this build was compiled to ship.
		Admit: func(candidate catalog.CatalogSnapshot) error {
			_, err := releasecatalog.BuiltInSource().ResolveSnapshot(candidate)
			return err
		},
		ResolveRoot: deps.resolveRoot,
		// Actors is deliberately unset: install writes the skill-only v1
		// representation. Converging it with update is a separate change.
	})
	if err != nil {
		return nil, nil, err
	}
	requests := make([]installtxn.GroupRequest, 0, len(candidates))
	for _, candidate := range candidates {
		observation, err := deps.observe(candidate.Plan, installobserve.DefaultOptions())
		if err != nil {
			return nil, nil, err
		}
		requests = append(requests, installtxn.GroupRequest{Plan: candidate.Plan, Observation: observation})
	}
	return requests, final.Results(), nil
}

func writeInstallResult(stdout, stderr io.Writer, operation, status, warning string, results []projection.RuntimeResult, applied bool, counts installtxn.Counts, code int) int {
	var output strings.Builder
	output.WriteString("operation=")
	output.WriteString(operation)
	output.WriteByte(' ')
	output.WriteString(status)
	if applied {
		output.WriteString(" create=")
		output.WriteString(strconv.Itoa(counts.Create))
		output.WriteString(" replace=")
		output.WriteString(strconv.Itoa(counts.Replace))
		output.WriteString(" remove=")
		output.WriteString(strconv.Itoa(counts.Remove))
		output.WriteString(" unchanged=")
		output.WriteString(strconv.Itoa(counts.Unchanged))
		output.WriteString(" preserve=")
		output.WriteString(strconv.Itoa(counts.Preserve))
	}
	output.WriteString(warning)
	output.WriteByte('\n')
	for _, result := range results {
		output.WriteString(installRuntimeLine(result.ID, result.Outcome, result.Action, applied && result.TouchAllowed))
	}
	if _, err := io.WriteString(stdout, output.String()); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	return code
}

func installRuntimeLine(id runtimematrix.RuntimeID, outcome runtimematrix.Outcome, action runtimematrix.Action, applied bool) string {
	line := "runtime=" + string(id)
	switch outcome {
	case runtimematrix.OutcomeAbsent:
		line += " presence=absent"
	case runtimematrix.OutcomeKnownIncompatible:
		line += " presence=present compatibility=incompatible"
	case runtimematrix.OutcomePresentCompatible:
		line += " presence=present compatibility=compatible"
	case runtimematrix.OutcomePresentUncertified:
		line += " presence=present compatibility=uncertified"
	default:
		line += " presence=present compatibility=unknown"
	}
	line += " action=" + string(action) + " touch=denied\n"
	if applied {
		line = strings.TrimSuffix(line, "denied\n") + "applied\n"
	}
	return line
}

func nextInstallBackupName() (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	return ".cortex-backup-" + hex.EncodeToString(bytes), nil
}

func validInstallBackupName(name string) bool {
	if filepath.Base(name) != name || len(name) != len(".cortex-backup-")+32 || !strings.HasPrefix(name, ".cortex-backup-") || name != strings.ToLower(name) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(name, ".cortex-backup-"))
	return err == nil
}

func existingDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
