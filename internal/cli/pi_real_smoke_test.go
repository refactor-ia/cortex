package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

var piCredentialAllowlist = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "OPENAI_API_KEY": true,
	"GOOGLE_API_KEY": true, "GEMINI_API_KEY": true, "OPENROUTER_API_KEY": true, "XAI_API_KEY": true,
}

var realSmokeRevision = regexp.MustCompile("^[0-9a-f]{40}$")

func TestPiRealSmoke(t *testing.T) {
	if !realSmokeAuthorized(os.Getenv("CORTEX_REAL_SMOKE_AUTHORIZATION"), piRealSmokeAuthorization) {
		t.Skip("Pi real smoke authorization is required")
	}
	root, err := os.MkdirTemp("", "cortex-real-smoke-")
	if err != nil {
		t.Fatal("real smoke temporary root unavailable")
	}
	evidence, runErr := runPiRealSmoke(root)
	cleanupErr := cleanupPiRealSmoke(root)
	if evidence != "" {
		t.Logf("%s cleanup=%t", evidence, cleanupErr == nil)
	}
	if cleanupErr != nil {
		t.Error("real smoke cleanup failed")
	}
	if runErr != nil {
		t.Fatal("Pi real smoke failed")
	}
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
	t.Run("credential configuration is allowlisted and secret-safe", func(t *testing.T) {
		lookup := func(name string) (string, bool) {
			return map[string]string{"ANTHROPIC_API_KEY": "secret-value"}[name], name == "ANTHROPIC_API_KEY"
		}
		for _, tc := range []struct {
			name, spec string
			ok         bool
		}{
			{"allowed", "ANTHROPIC_API_KEY", true}, {"unknown", "PRIVATE_KEY", false}, {"forbidden", "AWS_SECRET_ACCESS_KEY", false},
			{"missing", "OPENAI_API_KEY", false}, {"duplicate", "ANTHROPIC_API_KEY,ANTHROPIC_API_KEY", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				values, err := smokeCredentials(tc.spec, piCredentialAllowlist, lookup)
				if (err == nil) != tc.ok || err != nil && strings.Contains(err.Error(), "secret-value") || (tc.ok && len(values) != 1) {
					t.Fatalf("credential result = %q, %v", values, err)
				}
			})
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
func runPiRealSmoke(home string) (string, error) {
	sourceRevision := os.Getenv("CORTEX_REAL_SMOKE_SOURCE_REVISION")
	if !validSmokeRevision(sourceRevision) {
		return "", realSmokeError()
	}
	credentials, err := smokeCredentials(os.Getenv("CORTEX_REAL_SMOKE_CREDENTIAL_ENV"), piCredentialAllowlist, os.LookupEnv)
	if err != nil {
		return "", err
	}
	piPath, err := exec.LookPath(piSmokeBinary)
	if err != nil {
		return "", realSmokeError()
	}
	workdir, err := os.MkdirTemp(home, "work-")
	if err != nil {
		return "", realSmokeError()
	}
	env := piSmokeEnvironment(home, credentials)
	runner := &piRealSmokeRunner{path: piPath, env: env}
	reports, err := runtimeprobe.ProbeAll(context.Background(), runner)
	if err != nil || len(reports) != 3 || reports[0].RuntimeID() != runtimematrix.RuntimePi || reports[0].Status() != runtimeprobe.VersionDetected || reports[1].Status() != runtimeprobe.Absent || reports[2].Status() != runtimeprobe.Absent {
		return "", realSmokeError()
	}
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{reports[0].DetectedVersion()}}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode}})
	if err != nil {
		return "", realSmokeError()
	}
	deps := defaultInstallDependencies()
	deps.policy = policy
	deps.home = func() (string, error) { return home, nil }
	deps.resolveRoot = func(plan skilldest.Plan) (skillroot.Plan, error) {
		return skillroot.Resolve(plan, skillroot.Inputs{Home: home, PiCodingAgentDir: filepath.Join(home, ".pi", "agent")})
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, runner, deps); code != exitOK || stderr.Len() != 0 || stdout.String() != piSmokeInstallOutput() {
		return "", realSmokeError()
	}
	marker, fingerprint, err := smokeMarker()
	if err != nil {
		return "", err
	}
	installed, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "skills", "cortex-catalog-marker", "SKILL.md"))
	if err != nil || !bytes.Equal(installed, marker) || sha256.Sum256(installed) != sha256.Sum256(marker) {
		return "", realSmokeError()
	}
	info, err := os.Stat(filepath.Join(home, ".pi", "agent", "skills", "cortex-catalog-marker", "SKILL.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", realSmokeError()
	}
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", ".cortex", "install-state.json")); err != nil {
		return "", realSmokeError()
	}
	ctx, cancel := context.WithTimeout(context.Background(), piSmokeTimeout)
	defer cancel()
	startedAt := time.Now()
	execution, err := runPiSmokeCommand(ctx, piPath, workdir, env)
	durationMS := time.Since(startedAt).Milliseconds()
	if err != nil || ctx.Err() != nil || execution.ExitCode != 0 || len(execution.Stderr) != 0 || execution.StdoutOverflow || execution.StderrOverflow {
		return "", realSmokeError()
	}
	result, err := parseSmokeAcknowledgement(execution.Stdout)
	if err != nil || result.Name != "cortex-catalog-marker" || result.Heading != "Cortex Catalog Marker" {
		return "", realSmokeError()
	}
	return piSmokeEvidence(sourceRevision, reports[0].DetectedVersion(), fingerprint, marker, durationMS), nil
}
func realSmokeAuthorized(value, authorization string) bool { return value == authorization }
func cleanupPiRealSmoke(root string) error                 { return os.RemoveAll(root) }
func realSmokeError() error                                { return errors.New("real smoke validation failed") }
func smokeCredentials(spec string, allowlist map[string]bool, lookup func(string) (string, bool)) ([]string, error) {
	if spec == "" {
		return nil, nil
	}
	seen, values := map[string]bool{}, []string{}
	for _, name := range strings.Split(spec, ",") {
		value, ok := lookup(name)
		if !allowlist[name] || seen[name] || !ok || value == "" {
			return nil, realSmokeError()
		}
		seen[name] = true
		values = append(values, name+"="+value)
	}
	return values, nil
}
func piSmokeEnvironment(home string, credentials []string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, ".pi", "agent")}
	return append(env, credentials...)
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
	return "real_smoke source_revision_input=" + sourceRevision + " runtime=pi version=" + version + " snapshot=" + fingerprint + " marker_sha256=" + hex.EncodeToString(markerSum[:]) + " command_spec=" + hex.EncodeToString(commandSum[:]) + " installed=true ack=true duration_ms=" + strconv.FormatInt(durationMS, 10) + " timeout_ms=" + strconv.Itoa(piSmokeTimeoutMS) + " stdout_limit=" + strconv.Itoa(piSmokeOutputLimit) + " stderr_limit=" + strconv.Itoa(piSmokeOutputLimit) + " retries=0 exit=0 timeout=false stdout_overflow=false stderr_overflow=false"
}
func piSmokeCommandSpec() []string {
	return []string{"--no-session", "--no-extensions", "--no-prompt-templates", "--no-context-files", "--no-tools", "-p", piSmokePrompt}
}
func validSmokeRevision(value string) bool { return realSmokeRevision.MatchString(value) }
