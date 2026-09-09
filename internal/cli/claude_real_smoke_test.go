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
	claudeRealSmokeAuthorization = "issue-41-claude"
	claudeSmokeBinary            = "claude"
	claudeSmokeOutputLimit       = 8 * 1024
	claudeSmokeTimeout           = 60 * time.Second
	claudeSmokeSchema            = `{"type":"object","required":["name","heading"],"additionalProperties":false,"properties":{"name":{"type":"string","const":"cortex-catalog-marker"},"heading":{"type":"string"}}}`
	claudeSmokePrompt            = "/cortex-catalog-marker\nReturn one JSON object containing the loaded skill name and its first Markdown heading without the leading #."
)

func TestClaudeRealSmoke(t *testing.T) {
	if !realSmokeAuthorized(os.Getenv("CORTEX_REAL_SMOKE_AUTHORIZATION"), claudeRealSmokeAuthorization) {
		t.Skip("Claude Code real smoke authorization is required")
	}
	root, err := os.MkdirTemp("", "cortex-real-smoke-")
	if err != nil {
		t.Log("failure_code=internal")
		t.Fatal("Claude Code real smoke failed")
	}
	evidence, runErr := runClaudeRealSmoke(root)
	cleanupErr := cleanupClaudeRealSmoke(root)
	if runErr == nil && cleanupErr == nil && evidence != "" {
		t.Logf("%s cleanup=true", evidence)
	}
	logSmokeFailure(t, runErr, cleanupErr, "Claude Code real smoke failed")
}

func TestClaudeRealSmokeHelpers(t *testing.T) {
	t.Run("gate requires exact authorization", func(t *testing.T) {
		for value, want := range map[string]bool{claudeRealSmokeAuthorization: true, "": false, "issue-41-claude ": false} {
			got := realSmokeAuthorized(value, claudeRealSmokeAuthorization)
			if got != want {
				t.Fatalf("authorization = %t, want %t", got, want)
			}
		}
	})

	t.Run("official result requires exact required fields", func(t *testing.T) {
		valid := `{"type":"result","version":"1","subtype":"success","is_error":false,"structured_output":{"name":"cortex-catalog-marker","heading":"Cortex Catalog Marker"},"usage":{}}`
		cases := []struct {
			name string
			body string
			ok   bool
		}{
			{"valid extra outer fields", valid, true},
			{"missing", `{"type":"result","subtype":"success","is_error":false}`, false},
			{"duplicate required", strings.Replace(valid, `"type":"result",`, `"type":"result","type":"result",`, 1), false},
			{"wrong type", strings.Replace(valid, `"type":"result"`, `"type":false`, 1), false},
			{"wrong subtype", strings.Replace(valid, `"success"`, `"error"`, 1), false},
			{"wrong is error", strings.Replace(valid, `"is_error":false`, `"is_error":true`, 1), false},
			{"null is error", strings.Replace(valid, `false`, `null`, 1), false},
			{"null structured output", `{"type":"result","subtype":"success","is_error":false,"structured_output":null}`, false},
			{"nonobject structured output", `{"type":"result","subtype":"success","is_error":false,"structured_output":[]}`, false},
			{"malformed", `{`, false},
			{"trailing outer", valid + ` {}`, false},
			{"wrong acknowledgement", strings.Replace(valid, `Cortex Catalog Marker`, `Wrong`, 1), false},
			{"trailing acknowledgement", strings.Replace(valid, `}}`, `} {}`, 1), false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := parseClaudeSmokeResult([]byte(tc.body))
				if (err == nil) != tc.ok {
					t.Fatalf("parse error = %v", err)
				}
			})
		}
	})

	t.Run("schema and command are stable and safe", func(t *testing.T) {
		if strings.Contains(claudeSmokeSchema, "Cortex Catalog Marker") || !strings.HasPrefix(claudeSmokePrompt, "/cortex-catalog-marker\n") {
			t.Fatal("unsafe schema or prompt")
		}
		var schema map[string]any
		if json.Unmarshal([]byte(claudeSmokeSchema), &schema) != nil || schema["additionalProperties"] != false {
			t.Fatal("invalid schema")
		}
		want := []string{"-p", "--output-format", "json", "--json-schema", claudeSmokeSchema, "--no-session-persistence", "--tools", "", "--disallowedTools", "mcp__*", claudeSmokePrompt}
		if got := claudeSmokeCommandSpec(); !equalStrings(got, want) {
			t.Fatalf("command = %q", got)
		}
	})

	t.Run("evidence uses the shared command specification", func(t *testing.T) {
		evidence := claudeSmokeEvidence(strings.Repeat("a", 40), "1.2.3", "snapshot", []byte("marker"), 12)
		command := append([]string{claudeSmokeBinary}, claudeSmokeCommandSpec()...)
		commandSum := sha256.Sum256([]byte(strings.Join(command, "\x00")))
		schemaSum := sha256.Sum256([]byte(claudeSmokeSchema))
		fields := []string{
			"runtime=claude-code", "source_revision_input=" + strings.Repeat("a", 40),
			"command_spec=" + hex.EncodeToString(commandSum[:]), "schema_spec=" + hex.EncodeToString(schemaSum[:]),
			"installed=true", "ack=true", "duration_ms=12", "timeout_ms=60000", "stdout_limit=8192", "stderr_limit=8192",
			"retries=0", "exit=0", "timeout=false", "stdout_overflow=false", "stderr_overflow=false",
		}
		for _, field := range fields {
			if !strings.Contains(evidence, field) {
				t.Fatalf("missing %q", field)
			}
		}
		if strings.Contains(evidence, claudeSmokePrompt) || strings.Contains(evidence, claudeSmokeSchema) {
			t.Fatal("evidence leaks process input")
		}
	})

	t.Run("temporary root cleanup removes its target", func(t *testing.T) {
		root := t.TempDir()
		if err := cleanupClaudeRealSmoke(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("temporary root remains")
		}
	})
}

func runClaudeRealSmoke(home string) (string, error) {
	revision := os.Getenv("CORTEX_REAL_SMOKE_SOURCE_REVISION")
	if !validSmokeRevision(revision) {
		return "", newSmokeFailure(smokeFailureInvalidInput)
	}
	path, err := exec.LookPath(claudeSmokeBinary)
	if err != nil {
		return "", newSmokeFailure(smokeFailureBinaryLookup)
	}
	workdir, err := os.MkdirTemp(home, "work-")
	if err != nil {
		return "", newSmokeFailure(smokeFailureInternal)
	}

	env := claudeSmokeEnvironment(home)
	runner := &claudeRealSmokeRunner{path: path, env: env}
	reports, err := runtimeprobe.ProbeAll(context.Background(), runner)
	if err != nil || len(reports) != 3 || reports[0].Status() != runtimeprobe.Absent || reports[1].Status() != runtimeprobe.Absent || reports[2].RuntimeID() != runtimematrix.RuntimeClaudeCode || reports[2].Status() != runtimeprobe.VersionDetected {
		return "", newSmokeFailure(smokeFailureProbe)
	}
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{{ID: runtimematrix.RuntimePi}, {ID: runtimematrix.RuntimeOpenCode}, {ID: runtimematrix.RuntimeClaudeCode, CertifiedCompatible: []string{reports[2].DetectedVersion()}}})
	if err != nil {
		return "", newSmokeFailure(smokeFailureInstall)
	}
	deps := defaultInstallDependencies()
	deps.policy = policy
	deps.home = func() (string, error) { return home, nil }
	deps.resolveRoot = func(plan skilldest.Plan) (skillroot.Plan, error) {
		return skillroot.Resolve(plan, skillroot.Inputs{Home: home})
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runWithInstallDependencies(context.Background(), []string{"install"}, &stdout, &stderr, runner, deps)
	if code != exitOK || stderr.Len() != 0 || stdout.String() != claudeSmokeInstallOutput() {
		return "", newSmokeFailure(smokeFailureInstall)
	}
	marker, snapshot, err := smokeMarker()
	if err != nil {
		return "", newSmokeFailure(smokeFailureInternal)
	}
	installedPath := filepath.Join(home, ".claude", "skills", "cortex-catalog-marker", "SKILL.md")
	installed, err := os.ReadFile(installedPath)
	if err != nil || !bytes.Equal(installed, marker) || sha256.Sum256(installed) != sha256.Sum256(marker) {
		return "", newSmokeFailure(smokeFailureReadback)
	}
	info, err := os.Stat(installedPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		return "", newSmokeFailure(smokeFailureReadback)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", ".cortex", "install-state.json")); err != nil {
		return "", newSmokeFailure(smokeFailureReadback)
	}

	ctx, cancel := context.WithTimeout(context.Background(), claudeSmokeTimeout)
	defer cancel()
	started := time.Now()
	execution, err := runClaudeSmokeCommand(ctx, path, workdir, env)
	duration := time.Since(started).Milliseconds()
	if err := smokeInvocationFailure(ctx, execution, err); err != nil {
		return "", err
	}
	result, err := parseClaudeSmokeResult(execution.Stdout)
	if err := smokeResultFailure(result, err); err != nil {
		return "", err
	}
	return claudeSmokeEvidence(revision, reports[2].DetectedVersion(), snapshot, marker, duration), nil
}

func cleanupClaudeRealSmoke(root string) error {
	return os.RemoveAll(root)
}
func claudeSmokeEnvironment(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "LC_ALL=C", "LANG=C", "NO_COLOR=1", "TERM=dumb", "HOME=" + home,
		"CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CLAUDE_CODE_SKIP_PROMPT_HISTORY=1", "DISABLE_AUTOUPDATER=1",
		"DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
}

type claudeRealSmokeRunner struct {
	path string
	env  []string
	done bool
	run  runtimeprobe.Execution
	err  error
}

func (r *claudeRealSmokeRunner) Lookup(name string) (string, error) {
	if name == claudeSmokeBinary {
		return r.path, nil
	}
	return "", exec.ErrNotFound
}

func (r *claudeRealSmokeRunner) Run(ctx context.Context, path string, args, _ []string) (runtimeprobe.Execution, error) {
	if path != r.path || !equalStrings(args, []string{"--version"}) {
		return runtimeprobe.Execution{}, realSmokeError()
	}
	if !r.done {
		r.run, r.err = runClaudeCommand(ctx, path, args, "", r.env)
		r.done = true
	}
	return r.run, r.err
}

func claudeSmokeCommandSpec() []string {
	return []string{"-p", "--output-format", "json", "--json-schema", claudeSmokeSchema, "--no-session-persistence", "--tools", "", "--disallowedTools", "mcp__*", claudeSmokePrompt}
}

func runClaudeSmokeCommand(ctx context.Context, path, dir string, env []string) (runtimeprobe.Execution, error) {
	return runClaudeCommand(ctx, path, claudeSmokeCommandSpec(), dir, env)
}

func runClaudeCommand(ctx context.Context, path string, args []string, dir string, env []string) (runtimeprobe.Execution, error) {
	stdout := &boundedSmokeOutput{limit: claudeSmokeOutputLimit}
	stderr := &boundedSmokeOutput{limit: claudeSmokeOutputLimit}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = dir
	command.Env = env
	command.Stdout = stdout
	command.Stderr = stderr
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

func parseClaudeSmokeResult(data []byte) (smokeAcknowledgement, error) {
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
		duplicateRequired := seen[name] && (name == "type" || name == "subtype" || name == "is_error" || name == "structured_output")
		if err != nil || !ok || duplicateRequired {
			return smokeAcknowledgement{}, realSmokeError()
		}
		seen[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return smokeAcknowledgement{}, realSmokeError()
		}
		switch name {
		case "type", "subtype":
			var value string
			if json.Unmarshal(raw, &value) != nil || (name == "type" && value != "result") || (name == "subtype" && value != "success") {
				return smokeAcknowledgement{}, realSmokeError()
			}
		case "is_error":
			var value any
			if json.Unmarshal(raw, &value) != nil {
				return smokeAcknowledgement{}, realSmokeError()
			}
			flag, ok := value.(bool)
			if !ok || flag {
				return smokeAcknowledgement{}, realSmokeError()
			}
		case "structured_output":
			if len(raw) == 0 || raw[0] != '{' {
				return smokeAcknowledgement{}, realSmokeError()
			}
			ack, parseErr := parseSmokeAcknowledgement(raw)
			if parseErr != nil || ack.Name != "cortex-catalog-marker" || ack.Heading != "Cortex Catalog Marker" {
				return smokeAcknowledgement{}, realSmokeError()
			}
			result = ack
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || !seen["type"] || !seen["subtype"] || !seen["is_error"] || !seen["structured_output"] {
		return smokeAcknowledgement{}, realSmokeError()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return smokeAcknowledgement{}, realSmokeError()
	}
	return result, nil
}

func claudeSmokeInstallOutput() string {
	return "operation=install status=completed touch=applied create=2 replace=0 remove=0 unchanged=0 preserve=0\n" +
		"runtime=pi presence=absent action=warn touch=denied\n" +
		"runtime=opencode presence=absent action=warn touch=denied\n" +
		"runtime=claude-code presence=present compatibility=compatible action=configure touch=applied\n"
}

func claudeSmokeEvidence(revision, version, snapshot string, marker []byte, duration int64) string {
	markerSum := sha256.Sum256(marker)
	command := append([]string{claudeSmokeBinary}, claudeSmokeCommandSpec()...)
	commandSum := sha256.Sum256([]byte(strings.Join(command, "\x00")))
	schemaSum := sha256.Sum256([]byte(claudeSmokeSchema))
	return "real_smoke source_revision_input=" + revision + " runtime=claude-code auth=subscription_keychain version=" + version + " snapshot=" + snapshot + " marker_sha256=" + hex.EncodeToString(markerSum[:]) + " command_spec=" + hex.EncodeToString(commandSum[:]) + " schema_spec=" + hex.EncodeToString(schemaSum[:]) + " installed=true ack=true duration_ms=" + strconv.FormatInt(duration, 10) + " timeout_ms=60000 stdout_limit=8192 stderr_limit=8192 retries=0 exit=0 timeout=false stdout_overflow=false stderr_overflow=false"
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
