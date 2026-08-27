package runtimeprobe

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

type fakeRun struct {
	execution Execution
	err       error
}
type lookupResult struct {
	path string
	err  error
}
type call struct {
	name, path string
	args, env  []string
	deadline   bool
}
type fakeRunner struct {
	lookup map[string]lookupResult
	runs   map[string]fakeRun
	calls  []call
}

func (f *fakeRunner) Lookup(name string) (string, error) { v := f.lookup[name]; return v.path, v.err }
func (f *fakeRunner) Run(ctx context.Context, path string, args, env []string) (Execution, error) {
	_, deadline := ctx.Deadline()
	f.calls = append(f.calls, call{"", path, append([]string(nil), args...), append([]string(nil), env...), deadline})
	v := f.runs[path]
	return v.execution, v.err
}
func ready() *fakeRunner {
	return &fakeRunner{lookup: map[string]lookupResult{"pi": {"/bin/pi", nil}, "opencode": {"/bin/opencode", nil}, "claude": {"/bin/claude", nil}}, runs: map[string]fakeRun{"/bin/pi": {execution: Execution{Stdout: []byte("0.84.3\n")}}, "/bin/opencode": {execution: Execution{Stdout: []byte("1.18.21\r\n")}}, "/bin/claude": {execution: Execution{Stdout: []byte("2.1.243 (Claude Code)")}}}}
}
func TestProbeAllObservedShapesRemainUnknown(t *testing.T) {
	f := ready()
	reports, err := ProbeAll(context.Background(), f)
	if err != nil {
		t.Fatalf("ProbeAll() error = %v", err)
	}
	wantVersions := []string{"0.84.3", "1.18.21", "2.1.243"}
	for i, report := range reports {
		if report.Status() != VersionDetected || report.DetectedVersion() != wantVersions[i] {
			t.Fatalf("report %d = %s/%q", i, report.Status(), report.DetectedVersion())
		}
		if !f.calls[i].deadline || !reflect.DeepEqual(f.calls[i].args, []string{"--version"}) {
			t.Fatalf("call %d is not a bounded canonical version probe", i)
		}
	}
	if len(f.calls) != 3 || f.calls[0].path != "/bin/pi" || f.calls[1].path != "/bin/opencode" || f.calls[2].path != "/bin/claude" {
		t.Fatalf("calls = %#v", f.calls)
	}
	for _, key := range []string{"PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0"} {
		if !strings.Contains(strings.Join(f.calls[0].env, "\n"), key) {
			t.Fatalf("Pi env misses safeguard")
		}
	}
	for _, call := range f.calls {
		if strings.Contains(strings.Join(call.env, "\n"), "HOME=") || strings.Contains(strings.Join(call.env, "\n"), "TOKEN=") {
			t.Fatal("arbitrary environment forwarded")
		}
	}
	observations, err := Observations(reports)
	if err != nil {
		t.Fatalf("Observations() error = %v", err)
	}
	for _, observation := range observations {
		if !observation.Present || observation.Version != "" || observation.Compatibility != runtimematrix.CompatibilityUnknown {
			t.Fatalf("observation leaked compatibility: %#v", observation)
		}
	}
}
func TestProbeStatusesAndFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		lookup error
		run    fakeRun
		want   Status
	}{
		{"absent", exec.ErrNotFound, fakeRun{}, Absent}, {"lookup failure", errors.New("private /path"), fakeRun{}, ""},
		{"nonzero", nil, fakeRun{execution: Execution{ExitCode: 2}}, CommandFailed}, {"stderr", nil, fakeRun{execution: Execution{Stderr: []byte("bad")}}, CommandFailed},
		{"overflow", nil, fakeRun{execution: Execution{StdoutOverflow: true}}, CommandFailed}, {"start", nil, fakeRun{err: errors.New("secret")}, CommandFailed},
		{"timeout", nil, fakeRun{err: context.DeadlineExceeded}, TimedOut},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := ready()
			f.lookup["pi"] = lookupResult{"/bin/pi", tt.lookup}
			f.runs["/bin/pi"] = tt.run
			reports, err := ProbeAll(context.Background(), f)
			if tt.want == "" {
				if err == nil || reports != nil || strings.Contains(err.Error(), "path") {
					t.Fatalf("generic lookup failure = %v, %#v", err, reports)
				}
				return
			}
			if err != nil || reports[0].Status() != tt.want {
				t.Fatalf("result = %#v, %v", reports, err)
			}
		})
	}
	for index, name := range []string{"pi", "opencode", "claude"} {
		f := ready()
		f.lookup[name] = lookupResult{"", exec.ErrNotFound}
		reports, err := ProbeAll(context.Background(), f)
		if err != nil || reports[index].Status() != Absent {
			t.Fatal("runtime absence was not preserved")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if reports, err := ProbeAll(ctx, ready()); err == nil || reports != nil {
		t.Fatalf("cancellation = %#v, %v", reports, err)
	}
}
func TestStrictVersionParsing(t *testing.T) {
	valid := []string{"1.2.3", "1.2.3-rc.1+build.7"}
	invalid := []string{"v1.2.3", "01.2.3", "1.2.3-01", "1.2.3+bad_thing", " 1.2.3", "1.2.3\n\n", "1.2.3\r", "1.2", string(make([]byte, 129))}
	for _, line := range valid {
		if got, ok := parseVersion(runtimematrix.RuntimePi, []byte(line)); !ok || got != line {
			t.Fatalf("valid %q = %q, %t", line, got, ok)
		}
	}
	for _, line := range invalid {
		if _, ok := parseVersion(runtimematrix.RuntimePi, []byte(line)); ok {
			t.Fatalf("invalid accepted: %q", line)
		}
	}
	for _, line := range []string{"1.2.3 (Claude Code)", "1.2.3 (Claude)", "1.2.3 (Claude Code)\nextra"} {
		_, ok := parseVersion(runtimematrix.RuntimeClaudeCode, []byte(line))
		if ok != (line == "1.2.3 (Claude Code)") {
			t.Fatalf("Claude parse %q = %t", line, ok)
		}
	}
	if _, ok := parseVersion(runtimematrix.RuntimePi, []byte{0xff}); ok {
		t.Fatal("invalid UTF-8 accepted")
	}
}
func TestObservationsAreStrictDetachedAndCanonical(t *testing.T) {
	reports := []Report{{id: runtimematrix.RuntimePi, status: Absent}, {id: runtimematrix.RuntimeOpenCode, status: UnrecognizedOutput}, {id: runtimematrix.RuntimeClaudeCode, status: TimedOut}}
	first, err := Observations(reports)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Observations(reports)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("determinism = %#v, %v", second, err)
	}
	first[0].Present = true
	if second[0].Present {
		t.Fatal("observations share storage")
	}
	if _, err := runtimematrix.Decide(second); err != nil {
		t.Fatalf("matrix rejected: %v", err)
	}
	for _, bad := range [][]Report{reports[:2], {reports[1], reports[0], reports[2]}, {{id: runtimematrix.RuntimePi, status: VersionDetected}}} {
		if got, err := Observations(bad); err == nil || got != nil {
			t.Fatalf("invalid reports = %#v, %v", got, err)
		}
	}
}
func TestMinimalEnvironment(t *testing.T) {
	env := minimalEnv([]string{"PATH=/bin", "HOME=/private", "TOKEN=value", "TMPDIR=/tmp"}, true)
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"HOME=", "TOKEN="} {
		if strings.Contains(joined, forbidden) {
			t.Fatal("forbidden environment variable")
		}
	}
	for _, required := range []string{"PATH=", "TMPDIR=", "LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb", "PI_OFFLINE=1"} {
		if !strings.Contains(joined, required) {
			t.Fatal("missing minimal environment variable")
		}
	}
	buffer := &limitedBuffer{}
	buffer.Write(make([]byte, outputLimit+1))
	if len(buffer.bytes) != outputLimit || !buffer.overflow {
		t.Fatal("output cap failed")
	}
}
