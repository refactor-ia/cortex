package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimeprobe"
)

type fakeRun struct {
	execution runtimeprobe.Execution
	err       error
}

type fakeRunner struct {
	lookup map[string]error
	runs   map[string]fakeRun
	calls  []string
}

func (f *fakeRunner) Lookup(name string) (string, error) {
	if err := f.lookup[name]; err != nil {
		return "", err
	}
	return "/private/" + name, nil
}

func (f *fakeRunner) Run(_ context.Context, path string, args, _ []string) (runtimeprobe.Execution, error) {
	f.calls = append(f.calls, path+" "+strings.Join(args, " "))
	run := f.runs[path]
	return run.execution, run.err
}

func readyRunner() *fakeRunner {
	return &fakeRunner{runs: map[string]fakeRun{
		"/private/pi":       {execution: runtimeprobe.Execution{Stdout: []byte("1.2.3\n")}},
		"/private/opencode": {execution: runtimeprobe.Execution{Stdout: []byte("2.3.4\n")}},
		"/private/claude":   {execution: runtimeprobe.Execution{Stdout: []byte("3.4.5 (Claude Code)")}},
	}}
}

func TestRunDoctor(t *testing.T) {
	tests := []struct {
		name       string
		runner     *fakeRunner
		wantCode   int
		wantStdout string
		wantStderr string
		assert     func(*testing.T, *fakeRunner, string)
	}{
		{
			name: "all runtimes absent",
			runner: &fakeRunner{lookup: map[string]error{
				"pi": exec.ErrNotFound, "opencode": exec.ErrNotFound, "claude": exec.ErrNotFound,
			}},
			wantCode: 0,
			wantStdout: "runtime=pi presence=absent action=warn touch=denied\n" +
				"runtime=opencode presence=absent action=warn touch=denied\n" +
				"runtime=claude-code presence=absent action=warn touch=denied\n",
		},
		{
			name:     "present runtimes remain unknown in canonical order",
			runner:   readyRunner(),
			wantCode: 2,
			wantStdout: "runtime=pi presence=present compatibility=unknown action=warn touch=denied\n" +
				"runtime=opencode presence=present compatibility=unknown action=warn touch=denied\n" +
				"runtime=claude-code presence=present compatibility=unknown action=warn touch=denied\n",
			assert: func(t *testing.T, runner *fakeRunner, output string) {
				t.Helper()
				if !reflect.DeepEqual(runner.calls, []string{"/private/pi --version", "/private/opencode --version", "/private/claude --version"}) {
					t.Fatalf("probe calls = %#v", runner.calls)
				}
				for _, forbidden := range []string{"1.2.3", "2.3.4", "3.4.5", "/private/"} {
					if strings.Contains(output, forbidden) {
						t.Fatalf("doctor output leaks %q: %q", forbidden, output)
					}
				}
			},
		},
		{
			name: "mixed present unknown and absent",
			runner: &fakeRunner{
				lookup: map[string]error{"opencode": exec.ErrNotFound},
				runs: map[string]fakeRun{
					"/private/pi":     {execution: runtimeprobe.Execution{Stdout: []byte("1.2.3\n")}},
					"/private/claude": {execution: runtimeprobe.Execution{Stderr: []byte("credential=private")}},
				},
			},
			wantCode: 2,
			wantStdout: "runtime=pi presence=present compatibility=unknown action=warn touch=denied\n" +
				"runtime=opencode presence=absent action=warn touch=denied\n" +
				"runtime=claude-code presence=present compatibility=unknown action=warn touch=denied\n",
			assert: func(t *testing.T, _ *fakeRunner, output string) {
				t.Helper()
				if strings.Contains(output, "credential=private") {
					t.Fatalf("doctor output leaks probe output: %q", output)
				}
			},
		},
		{
			name:       "bounded probe failure",
			runner:     &fakeRunner{lookup: map[string]error{"pi": errors.New("private lookup failure")}},
			wantCode:   70,
			wantStderr: "error=probe_failed\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(context.Background(), []string{"doctor"}, &stdout, &stderr, tt.runner); got != tt.wantCode {
				t.Fatalf("Run() exit code = %d, want %d", got, tt.wantCode)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
			if got := stderr.String(); got != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tt.wantStderr)
			}
			if tt.assert != nil {
				tt.assert(t, tt.runner, stdout.String())
			}
		})
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"doctor", "extra"}, {"install"}, {"uninstall"}, {"update"}} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := Run(context.Background(), args, &stdout, &stderr, readyRunner()); got != 64 {
				t.Fatalf("Run(%q) exit code = %d, want 64", args, got)
			}
			if stdout.Len() != 0 || stderr.String() != "error=invalid_command\n" {
				t.Fatalf("output = stdout %q stderr %q", stdout.String(), stderr.String())
			}
		})
	}
}
