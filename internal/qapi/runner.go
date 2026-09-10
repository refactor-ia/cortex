package qapi

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

const pipeWaitDelay = time.Second

type runFacts struct {
	invalid                 bool
	launchFailed, timedOut  bool
	exitNonZero, waitFailed bool
	stdout, stderr          []byte
	stdoutTruncated         bool
	stderrTruncated         bool
}

// runOnce starts exactly one already-bound Pi command. Its raw inputs and captures remain in memory.
func runOnce(ctx context.Context, invocation Invocation, framedInput []byte, timeout time.Duration) runFacts {
	if !validInvocation(invocation) || len(framedInput) == 0 || len(framedInput) > qaadmission.MaxRequestBytes || timeout < time.Duration(qaadmission.MinimumTimeoutSeconds)*time.Second || timeout > time.Duration(qaadmission.MaximumTimeoutSeconds)*time.Second {
		return runFacts{invalid: true}
	}

	input := bytes.Clone(framedInput)
	defer clear(input)
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := cappedWriter{maximum: qaadmission.MaxStdoutBytes}
	stderr := cappedWriter{maximum: qaadmission.MaxStderrBytes}
	command := exec.CommandContext(runContext, invocation.binary, invocation.argv...)
	command.Dir = invocation.cwd
	command.Env = minimalEnvironment()
	command.Stdin = bytes.NewReader(input)
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = pipeWaitDelay
	if err := command.Start(); err != nil {
		return runFacts{launchFailed: true}
	}

	waitErr := command.Wait()
	facts := runFacts{
		timedOut:        errors.Is(runContext.Err(), context.DeadlineExceeded),
		stdout:          stdout.bytes,
		stderr:          stderr.bytes,
		stdoutTruncated: stdout.truncated,
		stderrTruncated: stderr.truncated,
	}
	var exitErr *exec.ExitError
	facts.exitNonZero = errors.As(waitErr, &exitErr)
	facts.waitFailed = waitErr != nil && !facts.exitNonZero
	return facts
}

func validInvocation(invocation Invocation) bool {
	if !absolutePaths(invocation.binary, invocation.cwd) || len(invocation.argv) == 0 {
		return false
	}
	for _, argument := range invocation.argv {
		if argument == "" || strings.IndexByte(argument, 0) >= 0 {
			return false
		}
	}
	return true
}

func minimalEnvironment() []string {
	environment := []string{"LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb"}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	if runtime.GOOS == "windows" {
		for _, name := range []string{"SystemRoot", "ComSpec", "PATHEXT"} {
			if value, ok := os.LookupEnv(name); ok {
				environment = append(environment, name+"="+value)
			}
		}
	}
	return environment
}

type cappedWriter struct {
	maximum   int
	bytes     []byte
	truncated bool
}

func (writer *cappedWriter) Write(data []byte) (int, error) {
	written := len(data)
	remaining := writer.maximum - len(writer.bytes)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		writer.bytes = append(writer.bytes, data[:remaining]...)
	}
	if remaining < len(data) {
		writer.truncated = true
	}
	return written, nil
}
