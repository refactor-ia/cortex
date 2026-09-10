package qapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/qaadmission"
)

func TestCappedWriterAndMinimalEnvironment(t *testing.T) {
	writer := cappedWriter{maximum: 2}
	if written, err := writer.Write([]byte("abc")); written != 3 || err != nil || !writer.truncated || string(writer.bytes) != "ab" {
		t.Fatalf("capped write = %d, %v, %#v", written, err, writer)
	}

	t.Setenv("TMPDIR", "/synthetic/tmpdir")
	t.Setenv("TMP", "/synthetic/tmp")
	t.Setenv("TEMP", "/synthetic/temp")
	t.Setenv("PATH", "/synthetic/path")
	t.Setenv("PI_OFFLINE", "1")
	got := strings.Join(minimalEnvironment(), "\n")
	for _, expected := range []string{"LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb", "TMPDIR=/synthetic/tmpdir", "TMP=/synthetic/tmp", "TEMP=/synthetic/temp"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("minimal environment omitted %q: %q", expected, got)
		}
	}
	if strings.Contains(got, "PATH=") || strings.Contains(got, "PI_OFFLINE=") {
		t.Fatalf("minimal environment leaked ambient variables: %q", got)
	}
}

func TestRun(t *testing.T) {
	frame := []byte("framed request")
	for _, tc := range []struct {
		name     string
		scenario string
		check    func(t *testing.T, facts runFacts)
	}{
		{
			name:     "success uses one bound command with bounded stdin and environment",
			scenario: "success",
			check: func(t *testing.T, facts runFacts) {
				if facts.launchFailed || facts.timedOut || facts.exitNonZero || facts.waitFailed || facts.stdoutTruncated || facts.stderrTruncated {
					t.Fatalf("successful facts = %#v", facts)
				}
				if !strings.Contains(string(facts.stdout), "stdin="+sha256Text(frame)) || !strings.Contains(string(facts.stdout), "fixed=true path=true") {
					t.Fatalf("success stdout = %q", facts.stdout)
				}
			},
		},
		{
			name:     "nonzero exit is retained separately",
			scenario: "nonzero",
			check: func(t *testing.T, facts runFacts) {
				if !facts.exitNonZero || facts.launchFailed || facts.timedOut || facts.waitFailed || string(facts.stderr) != "nonzero=true\n" {
					t.Fatalf("nonzero facts = %#v", facts)
				}
			},
		},
		{
			name:     "silent success has no output",
			scenario: "silent",
			check: func(t *testing.T, facts runFacts) {
				if facts.launchFailed || facts.timedOut || facts.exitNonZero || facts.waitFailed || len(facts.stdout) != 0 || len(facts.stderr) != 0 {
					t.Fatalf("silent facts = %#v", facts)
				}
			},
		},
		{
			name:     "stdout and stderr are independently capped",
			scenario: "overflow",
			check: func(t *testing.T, facts runFacts) {
				if facts.launchFailed || facts.timedOut || facts.exitNonZero || facts.waitFailed || !facts.stdoutTruncated || !facts.stderrTruncated || len(facts.stdout) != qaadmission.MaxStdoutBytes || len(facts.stderr) != qaadmission.MaxStderrBytes {
					t.Fatalf("overflow facts = stdout=%d/%t stderr=%d/%t", len(facts.stdout), facts.stdoutTruncated, len(facts.stderr), facts.stderrTruncated)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := runOnce(context.Background(), testInvocation(t, tc.scenario), frame, 30*time.Second)
			tc.check(t, facts)
		})
	}
}

func TestRunRejectsInvalidInputBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invocation Invocation
		frame      []byte
		timeout    time.Duration
	}{
		{"zero invocation", Invocation{}, []byte("frame"), 30 * time.Second},
		{"empty frame", testInvocation(t, "success"), nil, 30 * time.Second},
		{"oversized frame", testInvocation(t, "success"), bytes.Repeat([]byte("x"), qaadmission.MaxRequestBytes+1), 30 * time.Second},
		{"too short timeout", testInvocation(t, "success"), []byte("frame"), 29 * time.Second},
		{"too long timeout", testInvocation(t, "success"), []byte("frame"), 3601 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := runOnce(context.Background(), tc.invocation, tc.frame, tc.timeout)
			if !facts.invalid || facts.launchFailed || facts.timedOut || facts.exitNonZero || facts.waitFailed {
				t.Fatalf("invalid input facts = %#v", facts)
			}
		})
	}
}

func TestRunWaitDelayReportsUndrainedDescendantPipe(t *testing.T) {
	started := time.Now()
	facts := runOnce(context.Background(), testInvocation(t, "descendant"), []byte("frame"), 30*time.Second)
	elapsed := time.Since(started)
	if !facts.waitFailed || facts.launchFailed || facts.timedOut || facts.exitNonZero || elapsed < pipeWaitDelay || elapsed > 3*time.Second {
		t.Fatalf("wait-delay facts = %#v after %s", facts, elapsed)
	}
}

func TestRunStartFailureAndParentDeadline(t *testing.T) {
	t.Run("start failure", func(t *testing.T) {
		invocation := testInvocation(t, "success")
		invocation.binary = filepath.Join(t.TempDir(), "missing")
		facts := runOnce(context.Background(), invocation, []byte("frame"), 30*time.Second)
		if !facts.launchFailed || facts.timedOut || facts.exitNonZero || facts.waitFailed {
			t.Fatalf("start failure facts = %#v", facts)
		}
	})
	t.Run("parent deadline does not wait for configured timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		facts := runOnce(ctx, testInvocation(t, "block"), []byte("frame"), 30*time.Second)
		if !facts.timedOut || facts.launchFailed || time.Since(started) > time.Second {
			t.Fatalf("deadline facts = %#v after %s", facts, time.Since(started))
		}
	})
}

func testInvocation(t *testing.T, scenario string) Invocation {
	t.Helper()
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return Invocation{
		binary: binary,
		cwd:    t.TempDir(),
		argv:   []string{"-test.run=^TestRunHelper$", "--", "qapi-run-helper", scenario},
	}
}

func TestRunHelper(t *testing.T) {
	index := 0
	for ; index < len(os.Args) && os.Args[index] != "qapi-run-helper"; index++ {
	}
	if index == len(os.Args) || index+1 == len(os.Args) {
		return
	}
	switch os.Args[index+1] {
	case "success":
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		fmt.Printf("stdin=%x cwd=%x fixed=%t path=%t\n", sha256.Sum256(input), sha256.Sum256([]byte(cwd)), fixedEnvironment(), os.Getenv("PATH") == "")
	case "nonzero":
		fmt.Fprintln(os.Stderr, "nonzero=true")
		os.Exit(7)
	case "silent":
	case "overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("o"), qaadmission.MaxStdoutBytes+1))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), qaadmission.MaxStderrBytes+1))
	case "block":
		time.Sleep(10 * time.Second)
	case "descendant":
		child := exec.Command(os.Args[0], "-test.run=^TestRunHelper$", "--", "qapi-run-helper", "child-sleep")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if child.Start() != nil {
			os.Exit(2)
		}
	case "child-sleep":
		time.Sleep(2 * time.Second)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func fixedEnvironment() bool {
	return os.Getenv("LC_ALL") == "C" && os.Getenv("LANG") == "C" && os.Getenv("NO_COLOR") == "1" && os.Getenv("TERM") == "dumb"
}

func sha256Text(value []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(value))
}
