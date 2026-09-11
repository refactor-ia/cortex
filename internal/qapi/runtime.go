package qapi

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

const maxVersionOutputBytes = 64 << 10

var versionProbeTimeout = 10 * time.Second

type versionCapture struct {
	started, startFailed, timedOut, waitFailed bool
	exitCode                                   int
	stdout, stderr                             []byte
	stdoutTruncated, stderrTruncated           bool
}

func fixedVersionCommand() ([]string, int, int) {
	return []string{"--version"}, maxVersionOutputBytes, maxVersionOutputBytes
}

// runVersionCommand runs only the fixed Pi identification command once.
func runVersionCommand(ctx context.Context, path, cwd string) versionCapture {
	arguments, stdoutLimit, stderrLimit := fixedVersionCommand()
	if !absolutePaths(path, cwd) {
		return versionCapture{startFailed: true}
	}
	runContext, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	stdout := cappedWriter{maximum: stdoutLimit}
	stderr := cappedWriter{maximum: stderrLimit}
	command := exec.CommandContext(runContext, path, arguments...)
	command.Dir = cwd
	command.Env = minimalEnvironment()
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = pipeWaitDelay
	if err := command.Start(); err != nil {
		return versionCapture{startFailed: true}
	}
	capture := versionCapture{started: true}
	waitErr := command.Wait()
	capture.stdout, capture.stderr = stdout.bytes, stderr.bytes
	capture.stdoutTruncated, capture.stderrTruncated = stdout.truncated, stderr.truncated
	capture.timedOut = runContext.Err() != nil
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		capture.exitCode = exitError.ExitCode()
	} else if waitErr != nil {
		capture.waitFailed = true
	}
	return capture
}
