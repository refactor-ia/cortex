// Package cli provides Cortex command orchestration without process ownership.
package cli

import (
	"context"
	"io"
	"strings"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
)

const (
	exitOK      = 0
	exitUnknown = 2
	exitUsage   = 64
	exitFailure = 70
)

// Run executes one Cortex command with the supplied runtime probe seam.
// A nil runner selects the constrained production system probe.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, runner runtimeprobe.Runner) int {
	if len(args) != 1 || args[0] != "doctor" {
		writeError(stderr, "invalid_command")
		return exitUsage
	}

	reports, err := probe(ctx, runner)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	observations, err := runtimeprobe.Observations(reports)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}
	matrix, err := runtimematrix.Decide(observations)
	if err != nil {
		writeError(stderr, "probe_failed")
		return exitFailure
	}

	var output strings.Builder
	unknown := false
	for _, decision := range matrix.Decisions {
		output.WriteString("runtime=")
		output.WriteString(string(decision.ID))
		if decision.Outcome == runtimematrix.OutcomeAbsent {
			output.WriteString(" presence=absent")
		} else {
			output.WriteString(" presence=present compatibility=unknown")
			unknown = true
		}
		output.WriteString(" action=")
		output.WriteString(string(decision.Action))
		output.WriteString(" touch=denied\n")
	}
	if _, err := io.WriteString(stdout, output.String()); err != nil {
		writeError(stderr, "output_failed")
		return exitFailure
	}
	if unknown {
		return exitUnknown
	}
	return exitOK
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
