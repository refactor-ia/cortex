package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/builtinassets"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillrender"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

const (
	piRealSmokeAuthorization = "issue-41-pi"
	piSmokeBinary            = "pi"
	piSmokeOutputLimit       = 8 * 1024
	piSmokeTimeoutMS         = 60 * 1000
	piSmokeTimeout           = time.Duration(piSmokeTimeoutMS) * time.Millisecond
	piSmokePrompt            = "/skill:cortex-catalog-marker\nRespond with exactly one minified JSON object containing the activated skill name and first Markdown heading."
)

var realSmokeRevision = regexp.MustCompile("^[0-9a-f]{40}$")

type smokeFailureCode string

const (
	smokeFailureInvalidInput   smokeFailureCode = "invalid_input"
	smokeFailureAuthCopy       smokeFailureCode = "auth_copy"
	smokeFailureBinaryLookup   smokeFailureCode = "binary_lookup"
	smokeFailureProbe          smokeFailureCode = "probe"
	smokeFailureInstall        smokeFailureCode = "install"
	smokeFailureReadback       smokeFailureCode = "readback"
	smokeFailureInvokeTimeout  smokeFailureCode = "invoke_timeout"
	smokeFailureInvokeOverflow smokeFailureCode = "invoke_overflow"
	smokeFailureInvokeExit     smokeFailureCode = "invoke_exit"
	smokeFailureInvokeStderr   smokeFailureCode = "invoke_stderr"
	smokeFailureResultParse    smokeFailureCode = "result_parse"
	smokeFailureCleanup        smokeFailureCode = "cleanup"
	smokeFailureInternal       smokeFailureCode = "internal"
)

type smokeFailure struct{ code smokeFailureCode }

type smokeFailureReporter interface {
	Helper()
	Log(args ...any)
	Fatal(args ...any)
}

type smokeFailureRecorder struct{ events []string }

func (*smokeFailureRecorder) Helper() {}

func (recorder *smokeFailureRecorder) Log(args ...any) {
	recorder.events = append(recorder.events, fmt.Sprint(args...))
}

func (recorder *smokeFailureRecorder) Fatal(args ...any) {
	recorder.events = append(recorder.events, "fatal="+fmt.Sprint(args...))
}

func (smokeFailure) Error() string { return "real smoke validation failed" }

func validSmokeFailureCode(code smokeFailureCode) bool {
	switch code {
	case smokeFailureInvalidInput, smokeFailureAuthCopy, smokeFailureBinaryLookup, smokeFailureProbe,
		smokeFailureInstall, smokeFailureReadback, smokeFailureInvokeTimeout, smokeFailureInvokeOverflow,
		smokeFailureInvokeExit, smokeFailureInvokeStderr, smokeFailureResultParse, smokeFailureCleanup,
		smokeFailureInternal:
		return true
	default:
		return false
	}
}

func newSmokeFailure(code smokeFailureCode) error {
	if validSmokeFailureCode(code) {
		return smokeFailure{code: code}
	}
	return smokeFailure{code: smokeFailureInternal}
}

func smokeFailureCodeOf(err error) smokeFailureCode {
	if err == nil {
		return ""
	}
	var failure smokeFailure
	if errors.As(err, &failure) && validSmokeFailureCode(failure.code) {
		return failure.code
	}
	return smokeFailureInternal
}

func smokeInvocationFailure(ctx context.Context, execution runtimeprobe.Execution, err error) error {
	if (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) || errors.Is(err, context.DeadlineExceeded) {
		return newSmokeFailure(smokeFailureInvokeTimeout)
	}
	if execution.StdoutOverflow || execution.StderrOverflow {
		return newSmokeFailure(smokeFailureInvokeOverflow)
	}
	if execution.ExitCode != 0 {
		return newSmokeFailure(smokeFailureInvokeExit)
	}
	if len(execution.Stderr) != 0 {
		return newSmokeFailure(smokeFailureInvokeStderr)
	}
	if err != nil {
		return newSmokeFailure(smokeFailureInternal)
	}
	return nil
}

func smokeResultFailure(result smokeAcknowledgement, err error) error {
	if err != nil || result.Name != "cortex-catalog-marker" || result.Heading != "Cortex Catalog Marker" {
		return newSmokeFailure(smokeFailureResultParse)
	}
	return nil
}

func smokeFailureLines(runErr, cleanupErr error) []string {
	lines := make([]string, 0, 2)
	if cleanupErr != nil {
		lines = append(lines, "failure_code="+string(smokeFailureCleanup))
	}
	if runErr != nil {
		lines = append(lines, "failure_code="+string(smokeFailureCodeOf(runErr)))
	}
	return lines
}

func logSmokeFailure(t smokeFailureReporter, runErr, cleanupErr error, message string) {
	t.Helper()
	for _, line := range smokeFailureLines(runErr, cleanupErr) {
		t.Log(line)
	}
	if runErr != nil || cleanupErr != nil {
		t.Fatal(message)
	}
}

func TestPiRealSmoke(t *testing.T) {
	authorization := os.Getenv("CORTEX_REAL_SMOKE_AUTHORIZATION")
	if !realSmokeAuthorized(authorization, piRealSmokeAuthorization) {
		t.Skip("Pi real smoke authorization is required")
	}
	root, err := os.MkdirTemp("", "cortex-real-smoke-")
	if err != nil {
		t.Log("failure_code=internal")
		t.Fatal("Pi real smoke failed")
	}
	source, authorized := subscriptionAuthSourceAfterGate(authorization, piRealSmokeAuthorization, func() string {
		return os.Getenv("CORTEX_REAL_SMOKE_SUBSCRIPTION_AUTH_FILE")
	})
	if !authorized {
		t.Fatal("real smoke authorization unavailable")
	}
	evidence, runErr := runPiRealSmoke(root, source)
	cleanupErr := cleanupPiRealSmoke(root)
	if runErr == nil && cleanupErr == nil && evidence != "" {
		t.Logf("%s cleanup=true", evidence)
	}
	logSmokeFailure(t, runErr, cleanupErr, "Pi real smoke failed")
}
func TestSmokeFailureAttribution(t *testing.T) {
	t.Run("allowlisted and unknown failures are safe", func(t *testing.T) {
		codes := []smokeFailureCode{
			smokeFailureInvalidInput, smokeFailureAuthCopy, smokeFailureBinaryLookup, smokeFailureProbe,
			smokeFailureInstall, smokeFailureReadback, smokeFailureInvokeTimeout, smokeFailureInvokeOverflow,
			smokeFailureInvokeExit, smokeFailureInvokeStderr, smokeFailureResultParse, smokeFailureCleanup,
			smokeFailureInternal,
		}
		for _, code := range codes {
			t.Run(string(code), func(t *testing.T) {
				err := newSmokeFailure(code)
				if err.Error() != "real smoke validation failed" || smokeFailureCodeOf(err) != code {
					t.Fatalf("failure = %v, code = %q", err, smokeFailureCodeOf(err))
				}
			})
		}
		if got := smokeFailureCodeOf(errors.New("untrusted detail")); got != smokeFailureInternal {
			t.Fatalf("unknown failure code = %q", got)
		}
		if got := smokeFailureCodeOf(newSmokeFailure("unknown")); got != smokeFailureInternal {
			t.Fatalf("unknown typed code = %q", got)
		}
		for _, failure := range []smokeFailure{{}, {code: "unknown"}} {
			if got := smokeFailureCodeOf(failure); got != smokeFailureInternal {
				t.Fatalf("direct failure code = %q", got)
			}
		}
	})
	t.Run("invocation failures use safe precedence", func(t *testing.T) {
		deadline, cancel := context.WithCancel(context.Background())
		cancel()
		cases := []struct {
			name string
			ctx  context.Context
			exec runtimeprobe.Execution
			err  error
			want smokeFailureCode
		}{
			{"deadline context wins", contextDeadline(), runtimeprobe.Execution{StdoutOverflow: true, ExitCode: 1, Stderr: []byte("x")}, errors.New("x"), smokeFailureInvokeTimeout},
			{"deadline error wins", context.Background(), runtimeprobe.Execution{StdoutOverflow: true, ExitCode: 1, Stderr: []byte("x")}, context.DeadlineExceeded, smokeFailureInvokeTimeout},
			{"overflow wins", context.Background(), runtimeprobe.Execution{StdoutOverflow: true, ExitCode: 1, Stderr: []byte("x")}, nil, smokeFailureInvokeOverflow},
			{"exit wins", context.Background(), runtimeprobe.Execution{ExitCode: 1, Stderr: []byte("x")}, nil, smokeFailureInvokeExit},
			{"stderr wins", context.Background(), runtimeprobe.Execution{Stderr: []byte("x")}, nil, smokeFailureInvokeStderr},
			{"unexpected error is internal", deadline, runtimeprobe.Execution{}, errors.New("x"), smokeFailureInternal},
			{"success", context.Background(), runtimeprobe.Execution{}, nil, ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := smokeInvocationFailure(tc.ctx, tc.exec, tc.err); smokeFailureCodeOf(got) != tc.want {
					t.Fatalf("code = %q, want %q", smokeFailureCodeOf(got), tc.want)
				}
			})
		}
	})
	t.Run("failure lines follow cleanup-first order", func(t *testing.T) {
		cases := []struct {
			name  string
			run   error
			clean error
			want  []string
		}{
			{"run only", newSmokeFailure(smokeFailureProbe), nil, []string{"failure_code=probe"}},
			{"cleanup only", nil, errors.New("x"), []string{"failure_code=cleanup"}},
			{"both", newSmokeFailure(smokeFailureProbe), errors.New("x"), []string{"failure_code=cleanup", "failure_code=probe"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := smokeFailureLines(tc.run, tc.clean); !equalStrings(got, tc.want) {
					t.Fatalf("lines = %q, want %q", got, tc.want)
				}
			})
		}
	})
	t.Run("logs precede fatal in cleanup-first order", func(t *testing.T) {
		cases := []struct {
			name  string
			run   error
			clean error
			want  []string
		}{
			{"run only", newSmokeFailure(smokeFailureProbe), nil, []string{"failure_code=probe", "fatal=Pi real smoke failed"}},
			{"cleanup only", nil, errors.New("x"), []string{"failure_code=cleanup", "fatal=Pi real smoke failed"}},
			{"both", newSmokeFailure(smokeFailureProbe), errors.New("x"), []string{"failure_code=cleanup", "failure_code=probe", "fatal=Pi real smoke failed"}},
			{"success", nil, nil, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				recorder := &smokeFailureRecorder{}
				logSmokeFailure(recorder, tc.run, tc.clean, "Pi real smoke failed")
				if !equalStrings(recorder.events, tc.want) {
					t.Fatalf("events = %q, want %q", recorder.events, tc.want)
				}
			})
		}
	})

	t.Run("auth copy and parser boundaries are classified", func(t *testing.T) {
		if got := smokeFailureCodeOf(copySubscriptionAuth("relative", filepath.Join(t.TempDir(), "auth.json"))); got != smokeFailureAuthCopy {
			t.Fatalf("auth copy code = %q", got)
		}
		result, err := parseSmokeAcknowledgement([]byte("{"))
		if got := smokeFailureCodeOf(smokeResultFailure(result, err)); got != smokeFailureResultParse {
			t.Fatalf("parser boundary code = %q", got)
		}
	})
}

func contextDeadline() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	return ctx
}

func TestPiRealSmokeHelpers(t *testing.T) {
	t.Run("gate requires exact authorization", func(t *testing.T) {
		for value, want := range map[string]bool{piRealSmokeAuthorization: true, "": false, "issue-41-pi ": false} {
			if got := realSmokeAuthorized(value, piRealSmokeAuthorization); got != want {
				t.Fatalf("authorization = %t, want %t", got, want)
			}
		}
	})
	t.Run("JSON requires one exact object and EOF", func(t *testing.T) {
		for _, tc := range []struct {
			name, body string
			ok         bool
		}{
			{"valid", `{"name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"}`, true},
			{"malformed", `{`, false}, {"missing", `{"name":"cortex-catalog-marker"}`, false},
			{"duplicate key", `{"name":"cortex-catalog-marker","name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"}`, false},
			{"case variant", `{"Name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"}`, false},
			{"unknown", `{"name":"cortex-catalog-marker","heading":"Cortex Catalog Marker","extra":"value"}`, false},
			{"non-string value", `{"name":1,"heading":"Cortex Catalog Marker"}`, false},
			{"null value", `{"name":null,"heading":"Cortex Catalog Marker"}`, false},
			{"non-object", `["name","heading"]`, false},
			{"multiple", `{"name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"} {"name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"}`, false},
			{"trailing", `{} trailing`, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := parseSmokeAcknowledgement([]byte(tc.body))
				if (err == nil) != tc.ok {
					t.Fatalf("parse error = %v", err)
				}
			})
		}
	})
	t.Run("marker matches rendered catalog skill", func(t *testing.T) {
		snapshot, err := builtinassets.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := skillrender.Render(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var expected []byte
		for _, skill := range rendered.Skills() {
			if skill.LogicalID() == "skills/catalog-marker" && skill.CapabilityID() == "catalog-marker" {
				expected = skill.Content()
			}
		}
		marker, fingerprint, err := smokeMarker()
		if err != nil || !bytes.Equal(marker, expected) || fingerprint != snapshot.Fingerprint() {
			t.Fatalf("marker = (%q, %q, %v), want rendered catalog marker", marker, fingerprint, err)
		}
	})
	t.Run("evidence qualifies process inputs and bounds", func(t *testing.T) {
		evidence := piSmokeEvidence(strings.Repeat("a", 40), "1.2.3", "fingerprint", []byte("marker"), 12)
		for _, field := range []string{
			"source_revision_input=" + strings.Repeat("a", 40), "duration_ms=12", "timeout_ms=60000",
			"stdout_limit=8192", "stderr_limit=8192", "retries=0", "exit=0", "timeout=false",
			"stdout_overflow=false", "stderr_overflow=false",
		} {
			if !strings.Contains(evidence, field) {
				t.Fatalf("evidence missing %q: %q", field, evidence)
			}
		}
		if strings.Contains(evidence, "source_revision=") {
			t.Fatalf("evidence uses obsolete revision claim: %q", evidence)
		}
	})
	t.Run("bounded output records overflow", func(t *testing.T) {
		buffer := &boundedSmokeOutput{limit: 3}
		if _, err := buffer.Write([]byte("abcd")); err != nil || string(buffer.bytes) != "abc" || !buffer.overflow {
			t.Fatalf("bounded output = %#v, %v", buffer, err)
		}
	})
	t.Run("temporary root cleanup removes its target", func(t *testing.T) {
		root := t.TempDir()
		if err := cleanupPiRealSmoke(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temporary root remains")
		}
	})
}
func runPiRealSmoke(home, subscriptionAuthSource string) (string, error) {
	sourceRevision := os.Getenv("CORTEX_REAL_SMOKE_SOURCE_REVISION")
	if !validSmokeRevision(sourceRevision) {
		return "", newSmokeFailure(smokeFailureInvalidInput)
	}
	if err := copySubscriptionAuth(subscriptionAuthSource, piSubscriptionAuthTarget(home)); err != nil {
		return "", err
	}
	piPath, err := exec.LookPath(piSmokeBinary)
	if err != nil {
		return "", newSmokeFailure(smokeFailureBinaryLookup)
	}
	workdir, err := os.MkdirTemp(home, "work-")
	if err != nil {
		return "", newSmokeFailure(smokeFailureInternal)
	}
	env := piSmokeEnvironment(home)
	runner := &piRealSmokeRunner{path: piPath, env: env}
	reports, err := runtimeprobe.ProbeAll(context.Background(), runner)
	if err != nil || len(reports) != 3 || reports[0].RuntimeID() != runtimematrix.RuntimePi || reports[0].Status() != runtimeprobe.VersionDetected || reports[1].Status() != runtimeprobe.Absent || reports[2].Status() != runtimeprobe.Absent {
		return "", newSmokeFailure(smokeFailureProbe)
	}
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{reports[0].DetectedVersion()}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}})
	if err != nil {
		return "", newSmokeFailure(smokeFailureInstall)
	}
	deps := defaultInstallDependencies()
	deps.policy = policy
	deps.home = func() (string, error) { return home, nil }
	deps.resolveRoot = func(plan skilldest.Plan) (skillroot.Plan, error) {
		return skillroot.Resolve(plan, skillroot.Inputs{Home: home, PiCodingAgentDir: filepath.Join(home, ".pi", "agent")})
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, runner, deps); code != exitOK || stderr.Len() != 0 || stdout.String() != piSmokeInstallOutput() {
		return "", newSmokeFailure(smokeFailureInstall)
	}
	marker, fingerprint, err := smokeMarker()
	if err != nil {
		return "", newSmokeFailure(smokeFailureInternal)
	}
	installed, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "skills", "cortex-catalog-marker", "SKILL.md"))
	if err != nil || !bytes.Equal(installed, marker) || sha256.Sum256(installed) != sha256.Sum256(marker) {
		return "", newSmokeFailure(smokeFailureReadback)
	}
	info, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills", "cortex-catalog-marker", "SKILL.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", newSmokeFailure(smokeFailureReadback)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", ".cortex", "install-state.json")); err != nil {
		return "", newSmokeFailure(smokeFailureReadback)
	}
	ctx, cancel := context.WithTimeout(context.Background(), piSmokeTimeout)
	defer cancel()
	startedAt := time.Now()
	execution, err := runPiSmokeCommand(ctx, piPath, workdir, env)
	durationMS := time.Since(startedAt).Milliseconds()
	if err := smokeInvocationFailure(ctx, execution, err); err != nil {
		return "", err
	}
	result, err := parseSmokeAcknowledgement(execution.Stdout)
	if err := smokeResultFailure(result, err); err != nil {
		return "", err
	}
	return piSmokeEvidence(sourceRevision, reports[0].DetectedVersion(), fingerprint, marker, durationMS), nil
}
func realSmokeAuthorized(value, authorization string) bool { return value == authorization }
func cleanupPiRealSmoke(root string) error                 { return os.RemoveAll(root) }
func realSmokeError() error                                { return newSmokeFailure(smokeFailureInternal) }
func piSmokeEnvironment(home string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent")}
}

type piRealSmokeRunner struct {
	path string
	env  []string
	done bool
	run  runtimeprobe.Execution
	err  error
}

func (runner *piRealSmokeRunner) Lookup(name string) (string, error) {
	if name == piSmokeBinary {
		return runner.path, nil
	}
	return "", exec.ErrNotFound
}
func (runner *piRealSmokeRunner) Run(ctx context.Context, path string, args, _ []string) (runtimeprobe.Execution, error) {
	if path != runner.path || len(args) != 1 || args[0] != "--version" {
		return runtimeprobe.Execution{}, realSmokeError()
	}
	if runner.done {
		return runner.run, runner.err
	}
	runner.run, runner.err = runPiCommand(ctx, path, args, "", runner.env)
	runner.done = true
	return runner.run, runner.err
}

type boundedSmokeOutput struct {
	bytes    []byte
	limit    int
	overflow bool
}

func (buffer *boundedSmokeOutput) Write(value []byte) (int, error) {
	remaining := buffer.limit - len(buffer.bytes)
	if remaining > 0 {
		if len(value) > remaining {
			buffer.bytes = append(buffer.bytes, value[:remaining]...)
			buffer.overflow = true
		} else {
			buffer.bytes = append(buffer.bytes, value...)
		}
	} else if len(value) > 0 {
		buffer.overflow = true
	}
	return len(value), nil
}
func runPiSmokeCommand(ctx context.Context, path, dir string, env []string) (runtimeprobe.Execution, error) {
	return runPiCommand(ctx, path, piSmokeCommandSpec(), dir, env)
}
func runPiCommand(ctx context.Context, path string, args []string, dir string, env []string) (runtimeprobe.Execution, error) {
	stdout, stderr := &boundedSmokeOutput{limit: piSmokeOutputLimit}, &boundedSmokeOutput{limit: piSmokeOutputLimit}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir, command.Env, command.Stdout, command.Stderr = dir, env, stdout, stderr
	err := command.Run()
	execution := runtimeprobe.Execution{Stdout: append([]byte(nil), stdout.bytes...), Stderr: append([]byte(nil), stderr.bytes...), StdoutOverflow: stdout.overflow, StderrOverflow: stderr.overflow}
	if err == nil {
		return execution, nil
	}
	if ctx.Err() != nil {
		return execution, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		execution.ExitCode = exit.ExitCode()
		return execution, nil
	}
	return execution, realSmokeError()
}

type smokeAcknowledgement struct{ Name, Heading string }

func parseSmokeAcknowledgement(data []byte) (smokeAcknowledgement, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return smokeAcknowledgement{}, realSmokeError()
	}
	seen := map[string]bool{}
	var result smokeAcknowledgement
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] || (name != "name" && name != "heading") {
			return smokeAcknowledgement{}, realSmokeError()
		}
		seen[name] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || len(raw) < 2 || raw[0] != '"' {
			return smokeAcknowledgement{}, realSmokeError()
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return smokeAcknowledgement{}, realSmokeError()
		}
		if name == "name" {
			result.Name = value
		} else {
			result.Heading = value
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || result.Name == "" || result.Heading == "" {
		return smokeAcknowledgement{}, realSmokeError()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return smokeAcknowledgement{}, realSmokeError()
	}
	return result, nil
}
func smokeMarker() ([]byte, string, error) {
	snapshot, err := builtinassets.Snapshot()
	if err != nil {
		return nil, "", realSmokeError()
	}
	rendered, err := skillrender.Render(snapshot)
	if err != nil {
		return nil, "", realSmokeError()
	}
	for _, skill := range rendered.Skills() {
		if skill.LogicalID() == "skills/catalog-marker" && skill.CapabilityID() == "catalog-marker" {
			return skill.Content(), snapshot.Fingerprint(), nil
		}
	}
	return nil, "", realSmokeError()
}
func piSmokeInstallOutput() string {
	return "operation=install status=completed touch=applied create=2 replace=0 remove=0 unchanged=0 preserve=0\n" +
		"runtime=pi presence=present compatibility=compatible action=configure touch=applied\n" +
		"runtime=opencode presence=absent action=warn touch=denied\n" +
		"runtime=claude-code presence=absent action=warn touch=denied\n"
}
func piSmokeEvidence(sourceRevision, version, fingerprint string, marker []byte, durationMS int64) string {
	command := append([]string{piSmokeBinary}, piSmokeCommandSpec()...)
	markerSum, commandSum := sha256.Sum256(marker), sha256.Sum256([]byte(strings.Join(command, "\x00")))
	return "real_smoke source_revision_input=" + sourceRevision + " runtime=pi auth=subscription_copy version=" + version + " snapshot=" + fingerprint + " marker_sha256=" + hex.EncodeToString(markerSum[:]) + " command_spec=" + hex.EncodeToString(commandSum[:]) + " installed=true ack=true duration_ms=" + strconv.FormatInt(durationMS, 10) + " timeout_ms=" + strconv.Itoa(piSmokeTimeoutMS) + " stdout_limit=" + strconv.Itoa(piSmokeOutputLimit) + " stderr_limit=" + strconv.Itoa(piSmokeOutputLimit) + " retries=0 exit=0 timeout=false stdout_overflow=false stderr_overflow=false"
}
func piSmokeCommandSpec() []string {
	return []string{"--no-session", "--no-extensions", "--no-prompt-templates", "--no-context-files", "--no-tools", "-p", piSmokePrompt}
}
func validSmokeRevision(value string) bool { return realSmokeRevision.MatchString(value) }
