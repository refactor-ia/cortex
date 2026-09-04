package cli

import (
	"bufio"
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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/runtimeprobe"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

const (
	opencodeRealSmokeAuthorization = "issue-41-opencode"
	opencodeSmokeBinary            = "opencode"
	opencodeSmokeOutputLimit       = 8 * 1024
	opencodeSmokeTimeout           = 60 * time.Second
	opencodeSmokeConfig            = `{"permission":{"*":"deny","skill":{"cortex-catalog-marker":"allow"}}}`
	opencodeSmokePrompt            = "Use the cortex-catalog-marker skill. Return exactly one minified JSON object with lowercase keys name and heading. Set name to the loaded skill's declared name and heading to its first Markdown heading without the leading #. No commentary."
)

func TestOpenCodeRealSmoke(t *testing.T) {
	authorization := os.Getenv("CORTEX_REAL_SMOKE_AUTHORIZATION")
	if !realSmokeAuthorized(authorization, opencodeRealSmokeAuthorization) {
		t.Skip("OpenCode real smoke authorization is required")
	}
	root, err := os.MkdirTemp("", "cortex-real-smoke-")
	if err != nil {
		t.Fatal("real smoke temporary root unavailable")
	}
	source, authorized := subscriptionAuthSourceAfterGate(authorization, opencodeRealSmokeAuthorization, func() string {
		return os.Getenv("CORTEX_REAL_SMOKE_SUBSCRIPTION_AUTH_FILE")
	})
	if !authorized {
		t.Fatal("real smoke authorization unavailable")
	}
	evidence, runErr := runOpenCodeRealSmoke(root, source)
	cleanupErr := cleanupOpenCodeRealSmoke(root)
	if evidence != "" {
		t.Logf("%s cleanup=%t", evidence, cleanupErr == nil)
	}
	if cleanupErr != nil {
		t.Error("real smoke cleanup failed")
	}
	if runErr != nil {
		t.Fatal("OpenCode real smoke failed")
	}
}
func TestOpenCodeRealSmokeHelpers(t *testing.T) {
	t.Run("gate requires exact authorization", func(t *testing.T) {
		for value, want := range map[string]bool{opencodeRealSmokeAuthorization: true, "": false, "issue-41-opencode ": false} {
			if got := realSmokeAuthorized(value, opencodeRealSmokeAuthorization); got != want {
				t.Fatalf("authorization = %t, want %t", got, want)
			}
		}
	})
	t.Run("JSONL accepts completed skill and exact acknowledgement", func(t *testing.T) {
		valid := `{"type":"tool_use","timestamp":1700000000000,"sessionID":"s","part":{"type":"tool","tool":"skill","state":{"status":"completed"}}}` + "\n" +
			`{"type":"text","timestamp":1700000000001,"sessionID":"s","part":{"type":"text","text":"{\"name\":\"cortex-catalog-marker\",\"heading\":\"Cortex Catalog Marker\"}"}}`
		for _, tc := range []struct {
			name, body string
			ok         bool
		}{
			{"official numeric Date.now timestamp", valid, true}, {"ignored step event", `{"type":"step_start","timestamp":1700000000002,"sessionID":"s","part":{"type":"step"}}` + "\n" + valid, true}, {"malformed line", `{`, false},
			{"error event", `{"type":"error","timestamp":1700000000003,"sessionID":"s","part":{}}`, false},
			{"duplicate tool", valid + "\n" + strings.Split(valid, "\n")[0], false},
			{"wrong part type", strings.Replace(valid, `"type":"tool"`, `"type":"text"`, 1), false},
			{"incomplete tool", strings.Replace(valid, `"completed"`, `"running"`, 1), false},
			{"zero timestamp", strings.Replace(valid, `"timestamp":1700000000000`, `"timestamp":0`, 1), false},
			{"negative timestamp", strings.Replace(valid, `"timestamp":1700000000000`, `"timestamp":-1`, 1), false},
			{"fractional timestamp", strings.Replace(valid, `"timestamp":1700000000000`, `"timestamp":1.5`, 1), false},
			{"wrong acknowledgement", strings.Replace(valid, "Cortex Catalog Marker", "Wrong", 1), false},
			{"trailing acknowledgement", valid + "\n" + strings.Split(valid, "\n")[1], false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := parseOpenCodeSmokeJSONL([]byte(tc.body))
				if (err == nil) != tc.ok {
					t.Fatalf("parse error = %v", err)
				}
			})
		}
	})
	t.Run("evidence is bounded and does not disclose configuration", func(t *testing.T) {
		evidence := opencodeSmokeEvidence(strings.Repeat("a", 40), "1.2.3", "fingerprint", []byte("marker"), 12)
		for _, field := range []string{"source_revision_input=" + strings.Repeat("a", 40), "runtime=opencode", "duration_ms=12", "config_spec=", "skill_tool_completed=true", "retries=0", "exit=0", "timeout=false", "stdout_overflow=false", "stderr_overflow=false"} {
			if !strings.Contains(evidence, field) {
				t.Fatalf("evidence missing %q: %q", field, evidence)
			}
		}
		if strings.Contains(evidence, opencodeSmokeConfig) || strings.Contains(evidence, opencodeSmokePrompt) {
			t.Fatalf("evidence leaks process configuration: %q", evidence)
		}
		command := append([]string{opencodeSmokeBinary}, opencodeSmokeCommandSpec()...)
		commandSum := sha256.Sum256([]byte(strings.Join(command, "\x00")))
		if !strings.Contains(evidence, "command_spec="+hex.EncodeToString(commandSum[:])) {
			t.Fatalf("evidence command digest does not match command spec: %q", evidence)
		}
	})
	t.Run("temporary root cleanup removes its target", func(t *testing.T) {
		root := t.TempDir()
		if err := cleanupOpenCodeRealSmoke(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temporary root remains")
		}
	})
}
func runOpenCodeRealSmoke(home, subscriptionAuthSource string) (string, error) {
	sourceRevision := os.Getenv("CORTEX_REAL_SMOKE_SOURCE_REVISION")
	if !validSmokeRevision(sourceRevision) || copySubscriptionAuth(subscriptionAuthSource, opencodeSubscriptionAuthTarget(home)) != nil {
		return "", realSmokeError()
	}
	path, err := exec.LookPath(opencodeSmokeBinary)
	if err != nil {
		return "", realSmokeError()
	}
	workdir, err := os.MkdirTemp(home, "work-")
	if err != nil {
		return "", realSmokeError()
	}
	env := opencodeSmokeEnvironment(home)
	runner := &opencodeRealSmokeRunner{path: path, env: env}
	reports, err := runtimeprobe.ProbeAll(context.Background(), runner)
	if err != nil || len(reports) != 3 || reports[0].Status() != runtimeprobe.Absent || reports[1].RuntimeID() != runtimematrix.RuntimeOpenCode || reports[1].Status() != runtimeprobe.VersionDetected || reports[2].Status() != runtimeprobe.Absent {
		return "", realSmokeError()
	}
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{{ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimeOpenCode, CertifiedCompatible: []string{reports[1].DetectedVersion()}}, {ID: runtimematrix.RuntimeClaudeCode}})
	if err != nil {
		return "", realSmokeError()
	}
	deps := defaultInstallDependencies()
	deps.policy, deps.home = policy, func() (string, error) { return home, nil }
	deps.resolveRoot = func(plan skilldest.Plan) (skillroot.Plan, error) {
		return skillroot.Resolve(plan, skillroot.Inputs{Home: home})
	}
	var stdout, stderr bytes.Buffer
	if code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, runner, deps); code != exitOK || stderr.Len() != 0 || stdout.String() != opencodeSmokeInstallOutput() {
		return "", realSmokeError()
	}
	marker, snapshot, err := smokeMarker()
	if err != nil {
		return "", err
	}
	installedPath := filepath.Join(home, ".config", "opencode", "skills", "cortex-catalog-marker", "SKILL.md")
	installed, err := os.ReadFile(installedPath)
	if err != nil || !bytes.Equal(installed, marker) || sha256.Sum256(installed) != sha256.Sum256(marker) {
		return "", realSmokeError()
	}
	info, err := os.Stat(installedPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", realSmokeError()
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", ".cortex", "install-state.json")); err != nil {
		return "", realSmokeError()
	}
	ctx, cancel := context.WithTimeout(context.Background(), opencodeSmokeTimeout)
	defer cancel()
	startedAt := time.Now()
	execution, err := runOpenCodeSmokeCommand(ctx, path, workdir, env)
	durationMS := time.Since(startedAt).Milliseconds()
	if err != nil || ctx.Err() != nil || execution.ExitCode != 0 || len(execution.Stderr) != 0 || execution.StdoutOverflow || execution.StderrOverflow {
		return "", realSmokeError()
	}
	if _, err := parseOpenCodeSmokeJSONL(execution.Stdout); err != nil {
		return "", realSmokeError()
	}
	return opencodeSmokeEvidence(sourceRevision, reports[1].DetectedVersion(), snapshot, marker, durationMS), nil
}
func cleanupOpenCodeRealSmoke(root string) error { return os.RemoveAll(root) }
func opencodeSmokeEnvironment(home string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "LANG=C", "NO_COLOR=1", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"), "OPENCODE_CONFIG_DIR=" + filepath.Join(home, ".config", "opencode"), "OPENCODE_CONFIG_CONTENT=" + opencodeSmokeConfig}
}

type opencodeRealSmokeRunner struct {
	path string
	env  []string
	done bool
	run  runtimeprobe.Execution
	err  error
}

func (runner *opencodeRealSmokeRunner) Lookup(name string) (string, error) {
	if name == opencodeSmokeBinary {
		return runner.path, nil
	}
	return "", exec.ErrNotFound
}
func (runner *opencodeRealSmokeRunner) Run(ctx context.Context, path string, args, _ []string) (runtimeprobe.Execution, error) {
	if path != runner.path || len(args) != 1 || args[0] != "--version" {
		return runtimeprobe.Execution{}, realSmokeError()
	}
	if !runner.done {
		runner.run, runner.err = runOpenCodeCommand(ctx, path, args, "", runner.env)
		runner.done = true
	}
	return runner.run, runner.err
}
func opencodeSmokeCommandSpec() []string {
	return []string{"run", "--format", "json", opencodeSmokePrompt}
}
func runOpenCodeSmokeCommand(ctx context.Context, path, dir string, env []string) (runtimeprobe.Execution, error) {
	return runOpenCodeCommand(ctx, path, opencodeSmokeCommandSpec(), dir, env)
}
func runOpenCodeCommand(ctx context.Context, path string, args []string, dir string, env []string) (runtimeprobe.Execution, error) {
	stdout, stderr := &boundedSmokeOutput{limit: opencodeSmokeOutputLimit}, &boundedSmokeOutput{limit: opencodeSmokeOutputLimit}
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
func parseOpenCodeSmokeJSONL(data []byte) (smokeAcknowledgement, error) {
	var tool, text bool
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record struct {
			Type, SessionID string
			Timestamp       int64
			Part            json.RawMessage
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&record) != nil || record.Type == "" || record.Timestamp <= 0 || record.SessionID == "" || len(record.Part) == 0 || decoder.Decode(&struct{}{}) != io.EOF {
			return smokeAcknowledgement{}, realSmokeError()
		}
		var part struct {
			Type, Tool, Text string
			State            struct{ Status string }
		}
		if json.Unmarshal(record.Part, &part) != nil {
			return smokeAcknowledgement{}, realSmokeError()
		}
		switch record.Type {
		case "tool_use":
			if tool || part.Type != "tool" || part.Tool != "skill" || part.State.Status != "completed" {
				return smokeAcknowledgement{}, realSmokeError()
			}
			tool = true
		case "text":
			if text || part.Type != "text" {
				return smokeAcknowledgement{}, realSmokeError()
			}
			ack, err := parseSmokeAcknowledgement([]byte(part.Text))
			if err != nil || ack.Name != "cortex-catalog-marker" || ack.Heading != "Cortex Catalog Marker" {
				return smokeAcknowledgement{}, realSmokeError()
			}
			text = true
		case "step_start", "step_finish":
			if part.Type == "" {
				return smokeAcknowledgement{}, realSmokeError()
			}
		default:
			return smokeAcknowledgement{}, realSmokeError()
		}
	}
	if scanner.Err() != nil || !tool || !text {
		return smokeAcknowledgement{}, realSmokeError()
	}
	return smokeAcknowledgement{Name: "cortex-catalog-marker", Heading: "Cortex Catalog Marker"}, nil
}
func opencodeSmokeInstallOutput() string {
	return "operation=install status=completed touch=applied create=2 replace=0 remove=0 unchanged=0 preserve=0\n" +
		"runtime=pi presence=absent action=warn touch=denied\n" +
		"runtime=opencode presence=present compatibility=compatible action=configure touch=applied\n" +
		"runtime=claude-code presence=absent action=warn touch=denied\n"
}
func opencodeSmokeEvidence(revision, version, snapshot string, marker []byte, durationMS int64) string {
	markerSum, commandSum, configSum := sha256.Sum256(marker), sha256.Sum256([]byte(strings.Join(append([]string{opencodeSmokeBinary}, opencodeSmokeCommandSpec()...), "\x00"))), sha256.Sum256([]byte(opencodeSmokeConfig))
	return "real_smoke source_revision_input=" + revision + " runtime=opencode auth=subscription_copy version=" + version + " snapshot=" + snapshot + " marker_sha256=" + hex.EncodeToString(markerSum[:]) + " command_spec=" + hex.EncodeToString(commandSum[:]) + " config_spec=" + hex.EncodeToString(configSum[:]) + " installed=true skill_tool_completed=true ack=true duration_ms=" + strconv.FormatInt(durationMS, 10) + " timeout_ms=60000 stdout_limit=8192 stderr_limit=8192 retries=0 exit=0 timeout=false stdout_overflow=false stderr_overflow=false"
}
