package runtimeprobe

import (
	"context"
	"errors"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Status string

const (
	Absent             Status = "absent"
	VersionDetected    Status = "version_detected"
	UnrecognizedOutput Status = "unrecognized_output"
	CommandFailed      Status = "command_failed"
	TimedOut           Status = "timed_out"
)
const outputLimit = 4096

var errProbe = errors.New("runtime probe failed")

type Report struct {
	id      runtimematrix.RuntimeID
	status  Status
	version string
}

func (r Report) RuntimeID() runtimematrix.RuntimeID { return r.id }
func (r Report) Status() Status                     { return r.status }
func (r Report) DetectedVersion() string            { return r.version }

type Execution struct {
	ExitCode                       int
	Stdout, Stderr                 []byte
	StdoutOverflow, StderrOverflow bool
}
type Runner interface {
	Lookup(string) (string, error)
	Run(context.Context, string, []string, []string) (Execution, error)
}
type runtimeSpec struct {
	id   runtimematrix.RuntimeID
	name string
	pi   bool
}

var runtimeSpecs = [3]runtimeSpec{{runtimematrix.RuntimePi, "pi", true}, {runtimematrix.RuntimeOpenCode, "opencode", false}, {runtimematrix.RuntimeClaudeCode, "claude", false}}

func ProbeSystem(ctx context.Context) ([]Report, error) { return ProbeAll(ctx, systemRunner{}) }
func ProbeAll(ctx context.Context, runner Runner) ([]Report, error) {
	if ctx == nil || ctx.Err() != nil || runner == nil {
		return nil, errProbe
	}
	reports := make([]Report, 0, 3)
	for _, spec := range runtimeSpecs {
		path, err := runner.Lookup(spec.name)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				reports = append(reports, Report{id: spec.id, status: Absent})
				continue
			}
			return nil, errProbe
		}
		child, cancel := context.WithTimeout(ctx, 5*time.Second)
		execution, runErr := runner.Run(child, path, []string{"--version"}, minimalEnv(os.Environ(), spec.pi))
		childErr := child.Err()
		cancel()
		if ctx.Err() != nil {
			return nil, errProbe
		}
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(childErr, context.DeadlineExceeded) {
			reports = append(reports, Report{id: spec.id, status: TimedOut})
			continue
		}
		if runErr != nil || execution.ExitCode != 0 || len(execution.Stderr) != 0 || execution.StdoutOverflow || execution.StderrOverflow {
			reports = append(reports, Report{id: spec.id, status: CommandFailed})
			continue
		}
		if version, ok := parseVersion(spec.id, execution.Stdout); ok {
			reports = append(reports, Report{id: spec.id, status: VersionDetected, version: version})
		} else {
			reports = append(reports, Report{id: spec.id, status: UnrecognizedOutput})
		}
	}
	if ctx.Err() != nil {
		return nil, errProbe
	}
	return reports, nil
}
func Observations(reports []Report) ([]runtimematrix.Observation, error) {
	specs := runtimeSpecs
	if len(reports) != len(specs) {
		return nil, errProbe
	}
	observations := make([]runtimematrix.Observation, len(specs))
	for i, report := range reports {
		if report.id != specs[i].id || !validReport(report) {
			return nil, errProbe
		}
		observations[i] = runtimematrix.Observation{ID: report.id, Present: report.status != Absent, Compatibility: runtimematrix.CompatibilityUnknown}
	}
	if _, err := runtimematrix.Decide(observations); err != nil {
		return nil, errProbe
	}
	return observations, nil
}
func validReport(report Report) bool {
	return (report.status == Absent && report.version == "") || (report.status == VersionDetected && semver(report.version)) || ((report.status == UnrecognizedOutput || report.status == CommandFailed || report.status == TimedOut) && report.version == "")
}
func parseVersion(id runtimematrix.RuntimeID, output []byte) (string, bool) {
	if !utf8.Valid(output) {
		return "", false
	}
	line := strings.TrimSuffix(strings.TrimSuffix(string(output), "\r\n"), "\n")
	if strings.ContainsAny(line, "\r\n") {
		return "", false
	}
	if id == runtimematrix.RuntimeClaudeCode {
		const suffix = " (Claude Code)"
		if !strings.HasSuffix(line, suffix) {
			return "", false
		}
		line = strings.TrimSuffix(line, suffix)
	}
	if !semver(line) {
		return "", false
	}
	return line, true
}
func semver(version string) bool {
	const pattern = `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-((0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$`
	return len(version) > 0 && len(version) <= 128 && regexp.MustCompile(pattern).MatchString(version)
}

type systemRunner struct{}

func (systemRunner) Lookup(name string) (string, error) { return exec.LookPath(name) }
func (systemRunner) Run(ctx context.Context, path string, args, env []string) (Execution, error) {
	stdout, stderr := &limitedBuffer{}, &limitedBuffer{}
	command := exec.CommandContext(ctx, path, args...)
	command.Env, command.Stdout, command.Stderr = env, stdout, stderr
	err := command.Run()
	execution := Execution{Stdout: append([]byte(nil), stdout.bytes...), Stderr: append([]byte(nil), stderr.bytes...), StdoutOverflow: stdout.overflow, StderrOverflow: stderr.overflow}
	if err == nil {
		return execution, nil
	}
	if ctx.Err() != nil {
		return execution, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		execution.ExitCode = exitError.ExitCode()
		return execution, nil
	}
	return execution, err
}

type limitedBuffer struct {
	bytes    []byte
	overflow bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := outputLimit - len(b.bytes)
	if remaining > 0 {
		if len(value) > remaining {
			b.bytes = append(b.bytes, value[:remaining]...)
			b.overflow = true
		} else {
			b.bytes = append(b.bytes, value...)
		}
	} else if len(value) > 0 {
		b.overflow = true
	}
	return len(value), nil
}
func minimalEnv(source []string, pi bool) []string {
	allowed := map[string]bool{"PATH": true, "TMPDIR": true, "TMP": true, "TEMP": true, "SystemRoot": true, "ComSpec": true, "PATHEXT": true}
	values := make(map[string]string)
	for _, value := range source {
		key, _, found := strings.Cut(value, "=")
		if found && allowed[key] {
			values[key] = value
		}
	}
	env := make([]string, 0, len(values)+7)
	for _, key := range []string{"PATH", "TMPDIR", "TMP", "TEMP", "SystemRoot", "ComSpec", "PATHEXT"} {
		if value, found := values[key]; found {
			env = append(env, value)
		}
	}
	env = append(env, "LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb")
	if pi {
		env = append(env, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0")
	}
	return env
}
