package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/qaactor"
	"github.com/refactor-ia/cortex/internal/qapi"
	"github.com/refactor-ia/cortex/internal/qarole"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skilldest"
	"github.com/refactor-ia/cortex/internal/skillprojection"
	"github.com/refactor-ia/cortex/internal/skillrender"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "--model" || os.Args[1] == "--list-models" || os.Args[1] == "auth") {
		os.Exit(fakePi())
	}
	os.Exit(m.Run())
}

// fakePi is a deterministic fake Pi subprocess used only as test evidence; it
// never performs provider, authentication, or network work.
func fakePi() int {
	if os.Args[1] == "--version" {
		fmt.Println(qapi.PiSDKVersion)
		return 0
	}
	// The two out-of-band availability probes the report pipeline now runs
	// before it launches a run. The real Pi answers both; the fake has to
	// answer them too, or every scenario below stops at "unavailable" instead
	// of reaching the stream it exists to produce.
	if os.Args[1] == "--list-models" {
		fmt.Print("provider      model                context  max-out  thinking  images\n" +
			"nan           qwen3.6              262.1K   16.4K    yes       no\n" +
			"nan           glm5.3               262.1K   16.4K    yes       no\n" +
			"nan           deepseek-v4-flash    262.1K   16.4K    yes       no\n")
		return 0
	}
	if os.Args[1] == "auth" {
		fmt.Print(`{"status":"ready","provider":"nan","authType":"api_key"}` + "\n")
		return 0
	}
	switch scenario, err := os.ReadFile("qa-report-scenario"); {
	case err != nil:
		return 2
	case string(bytes.TrimSpace(scenario)) == "report":
		qaRecordRoleEvidence()
		message, _ := json.Marshal(map[string]any{
			"role":     "assistant",
			"content":  []any{map[string]any{"type": "text", "text": fixtureReport}},
			"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
		})
		fmt.Printf(`{"type":"session","version":3,"id":"fixture","timestamp":"1970-01-01T00:00:00.000Z","cwd":"fixture"}` + "\n" +
			`{"type":"agent_start"}` + "\n" + `{"type":"turn_start"}` + "\n" +
			`{"type":"message_start","message":{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"pending"}}` + "\n")
		fmt.Printf(`{"type":"message_end","message":%s}`+"\n"+`{"type":"turn_end","message":%s,"toolResults":[]}`+"\n", message, message)
		fmt.Printf(`{"type":"agent_end","messages":[%s],"willRetry":false}`+"\n"+`{"type":"agent_settled"}`+"\n", message)
		return 0
	case string(bytes.TrimSpace(scenario)) == "blank":
		// The sensitive marker rides inside validated stream fields; it must
		// never reach the CLI stderr diagnostic.
		message, _ := json.Marshal(map[string]any{
			"role":     "assistant",
			"content":  []any{map[string]any{"type": "text", "text": "   "}},
			"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
		})
		fmt.Printf(`{"type":"session","version":3,"id":"SENSITIVE-FIXTURE-MARKER","timestamp":"1970-01-01T00:00:00.000Z","cwd":"fixture"}` + "\n" +
			`{"type":"agent_start"}` + "\n" + `{"type":"turn_start"}` + "\n" +
			`{"type":"message_start","message":{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"pending"}}` + "\n")
		fmt.Printf(`{"type":"message_end","message":%s}`+"\n"+`{"type":"turn_end","message":%s,"toolResults":[]}`+"\n", message, message)
		fmt.Printf(`{"type":"agent_end","messages":[%s],"willRetry":false}`+"\n"+`{"type":"agent_settled"}`+"\n", message)
		return 0
	case string(bytes.TrimSpace(scenario)) == "truncated":
		fmt.Printf(`{"type":"session","version":3,"id":"fixture","timestamp":"1970-01-01T00:00:00.000Z","cwd":"fixture"}` + "\n" +
			`{"type":"agent_start"}` + "\n" + `{"type":"agent_end","messages":[],"willRetry":false,"secret":"SENSITIVE-FIXTURE-MARKER"}`)
		return 0
	case string(scenario) == "nonzero":
		return 7
	case string(scenario) == "block":
		time.Sleep(10 * time.Second)
	}
	return 2
}

// qaReportRoleEvidencePath is the bounded evidence file the fake Pi subprocess
// writes for one report run: line 1 is the first line of the framed input it
// received on stdin and line 2 is the --append-system-prompt actor path it was
// launched with.
const qaReportRoleEvidencePath = "qa-report-role-evidence"

// qaRecordRoleEvidence captures the role binding the CLI actually passed to
// this fake Pi subprocess. Recording is best-effort: it never alters the
// report stream, exit code, or anything outside the subprocess working
// directory, and the parent test asserts on the file only after the success
// path already passed.
func qaRecordRoleEvidence() {
	frame, err := io.ReadAll(os.Stdin)
	if err != nil {
		return
	}
	firstLine, _, _ := bytes.Cut(frame, []byte("\n"))
	actorPath := ""
	for index, argument := range os.Args {
		if argument == "--append-system-prompt" && index+1 < len(os.Args) {
			actorPath = os.Args[index+1]
		}
	}
	_ = os.WriteFile(qaReportRoleEvidencePath, []byte(string(firstLine)+"\n"+actorPath+"\n"), 0o600)
}

const fixtureTask = "Requirements to analyze:\n" +
	"Password minimum length is 8 characters\n" +
	"Passwords shorter than 12 characters must be rejected\n"

const fixtureReport = `Requirement cited: Password minimum length is 8 characters
Requirement cited: Passwords shorter than 12 characters must be rejected
Inconsistency: the supplied requirements disagree about the minimum password length (8 versus 12).
Resolution requested: confirm the authoritative minimum password length; no policy was invented.`

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

func mustOK(err error) {
	if err != nil {
		panic(err)
	}
}

type fixturePi struct{ path string }

func (fixture fixturePi) Resolve(context.Context) (string, error) { return fixture.path, nil }

// reportFixture materializes the installed requirements-analyst role from the
// production catalog and points the command at it with the fake Pi binary.
func reportFixture(t *testing.T) []string {
	return roleReportFixture(t, qarole.RequirementsAnalyst)
}

// roleReportFixture materializes one installed QA role from the production
// catalog — its actor file, its skill file, and the full six-role install
// state — and points the command at it with the fake Pi binary.
func roleReportFixture(t *testing.T, role qarole.RoleID) []string {
	t.Helper()
	roleName := string(role)
	snapshot := must(catalog.BuildCatalogSnapshot("../../catalog", "catalog.json", catalog.AdmissionPolicy{}))
	expected := must(qapi.CatalogAdmissionBinding(snapshot, role, "pi"))
	sources := must(qaactor.Sources(snapshot))
	binding := must(qaactor.Bind(must(qaactor.ProjectPi(must(qaactor.Render(sources))))))
	neutral := must(skillrender.Render(snapshot))
	skills := must(skillprojection.Build(runtimematrix.RuntimePi, neutral))
	var actor qaactor.ProjectedActor
	var skill skillprojection.ProjectedSkill
	for _, candidate := range binding.Actors() {
		if candidate.RoleID() == role {
			actor = candidate
		}
	}
	for _, candidate := range skills.Skills() {
		if candidate.CapabilityID() == roleName && candidate.LogicalID() == "skills/"+roleName {
			skill = candidate
		}
	}
	if actor.RoleID() != role || skill.LogicalID() != "skills/"+roleName {
		t.Fatalf("catalog projection does not contain role %q", roleName)
	}
	root := must(filepath.EvalSymlinks(t.TempDir()))
	cwd := must(filepath.EvalSymlinks(t.TempDir()))
	installRoot := filepath.Join(root, ".pi", "agent")
	const installation = installstate.InstallationID("000102030405060708090a0b0c0d0e0f")
	inputs := []installstate.V2ArtifactInput{{LogicalID: "skills/" + roleName, Kind: installstate.KindSkill, CapabilityID: roleName, RelativePath: "skills/cortex-" + roleName + "/SKILL.md", SHA256: expected.SkillSHA256, InstallationID: installation}}
	for _, entry := range qarole.Catalog() {
		actorHash := strings.Repeat("a", 64)
		if entry.ID == role {
			actorHash = expected.ActorSHA256
		}
		inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: "actors/" + string(entry.ID), Kind: installstate.KindPiActor, RoleID: entry.ID, ActorContractVersion: qaactor.ActorContractVersion, RelativePath: "agents/cortex-" + string(entry.ID) + ".md", SHA256: actorHash, InstallationID: installation})
	}
	state := must(installstate.NewV2(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, snapshot.Fingerprint(), installation, inputs))
	mustOK(os.MkdirAll(filepath.Join(installRoot, ".cortex"), 0o700))
	mustOK(os.MkdirAll(filepath.Join(installRoot, "agents"), 0o700))
	mustOK(os.MkdirAll(filepath.Join(installRoot, "skills", "cortex-"+roleName), 0o700))
	mustOK(os.WriteFile(filepath.Join(installRoot, ".cortex", "install-state.json"), must(installstate.Encode(state)), 0o600))
	mustOK(os.WriteFile(filepath.Join(installRoot, "agents", "cortex-"+roleName+".md"), actor.Content(), 0o600))
	mustOK(os.WriteFile(filepath.Join(installRoot, "skills", "cortex-"+roleName, "SKILL.md"), skill.Content(), 0o600))
	mustOK(os.WriteFile(filepath.Join(cwd, "requirements.txt"), []byte(fixtureTask), 0o600))
	mustOK(os.WriteFile(filepath.Join(cwd, "qa-report-scenario"), []byte("report"), 0o600))
	catalogRoot := must(filepath.Abs("../../catalog"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", root)
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = nil })
	t.Chdir(cwd)
	return []string{"qa", "run", "--role", roleName, "--request", "requirements.txt", "--catalog", catalogRoot}
}

func TestQARunReport(t *testing.T) {
	args := reportFixture(t)
	t.Run("success prints the substantive report without an admitted claim", func(t *testing.T) {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK {
			t.Fatalf("Run() = %d, stderr %q", code, stderr.String())
		}
		for _, expected := range []string{
			"Password minimum length is 8 characters",
			"Passwords shorter than 12 characters must be rejected",
			"Inconsistency:", "Resolution requested:",
		} {
			if !strings.Contains(stdout.String(), expected) {
				t.Fatalf("report is missing %q: %q", expected, stdout.String())
			}
		}
		if strings.Contains(stdout.String(), "admitted") || stderr.Len() != 0 {
			t.Fatalf("success claimed admission: stdout %q stderr %q", stdout.String(), stderr.String())
		}
	})
	t.Run("subprocess failure exits nonzero without an admitted claim", func(t *testing.T) {
		mustOK(os.WriteFile("qa-report-scenario", []byte("nonzero"), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "error=execution_failed") || strings.Contains(stderr.String(), "admitted") {
			t.Fatalf("failure = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("timeout exits nonzero without an admitted claim", func(t *testing.T) {
		mustOK(os.WriteFile("qa-report-scenario", []byte("block"), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if code := Run(ctx, args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "error=execution_timed_out") || strings.Contains(stderr.String(), "admitted") {
			t.Fatalf("timeout = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("blank report appends one bounded normalization diagnostic", func(t *testing.T) {
		mustOK(os.WriteFile("qa-report-scenario", []byte("blank"), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		expected := "error=normalization_failed\n" + qaReportNote + "normalization_stage=report normalization_reason=blank\n"
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 || stderr.String() != expected {
			t.Fatalf("blank report = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("truncated stream diagnostic stays bounded and sanitized", func(t *testing.T) {
		mustOK(os.WriteFile("qa-report-scenario", []byte("truncated"), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		expected := "error=normalization_failed\n" + qaReportNote + "normalization_stage=stream normalization_reason=unterminated\n"
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 || stderr.String() != expected ||
			strings.Contains(stderr.String(), "SENSITIVE-FIXTURE-MARKER") {
			t.Fatalf("truncated stream = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("invalid arguments exit with usage", func(t *testing.T) {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), []string{"qa", "run"}, stdout, stderr, nil); code != exitUsage ||
			!strings.Contains(stderr.String(), "error=invalid_arguments") || !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("usage = %d stderr %q", code, stderr.String())
		}
	})
}

// TestQARunSelectsEveryKnownRole exercises the closed six-role catalog through
// the CLI: each installed role must run the report fixture successfully and
// propagate its selected role into both the framed input and the bound actor.
func TestQARunSelectsEveryKnownRole(t *testing.T) {
	for _, role := range qarole.Catalog() {
		t.Run(string(role.ID), func(t *testing.T) {
			args := roleReportFixture(t, role.ID)
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK {
				t.Fatalf("Run(%s) = %d, stderr %q", role.ID, code, stderr.String())
			}
			for _, expected := range []string{
				"Password minimum length is 8 characters",
				"Passwords shorter than 12 characters must be rejected",
				"Inconsistency:", "Resolution requested:",
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("%s report is missing %q: %q", role.ID, expected, stdout.String())
				}
			}
			if strings.Contains(stdout.String(), "admitted") || stderr.Len() != 0 {
				t.Fatalf("%s success claimed admission: stdout %q stderr %q", role.ID, stdout.String(), stderr.String())
			}
			evidence, err := os.ReadFile(qaReportRoleEvidencePath)
			if err != nil {
				t.Fatalf("%s role evidence was not recorded: %v", role.ID, err)
			}
			lines := strings.Split(strings.TrimSuffix(string(evidence), "\n"), "\n")
			if len(lines) != 2 {
				t.Fatalf("%s role evidence is malformed: %q", role.ID, string(evidence))
			}
			if lines[0] != "/skill:cortex-"+string(role.ID) {
				t.Fatalf("%s role did not propagate into the framed input: %q", role.ID, lines[0])
			}
			if expectedActor := "agents/cortex-" + string(role.ID) + ".md"; !strings.HasSuffix(lines[1], expectedActor) {
				t.Fatalf("%s actor was not bound to the invocation: %q", role.ID, lines[1])
			}
		})
	}
}

// TestQARunRejectsUnknownAndEmptyRoles pins the argument-validation contract:
// role values outside the closed catalog and empty role values are rejected as
// invalid arguments with usage, before any request, catalog, or runtime work.
func TestQARunRejectsUnknownAndEmptyRoles(t *testing.T) {
	cases := []struct {
		name string
		role string
	}{
		{name: "unknown role is rejected", role: "nonexistent-role"},
		{name: "empty role is rejected", role: ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"qa", "run", "--role", tt.role, "--request", "requirements.txt", "--catalog", "catalog"}
			if code := Run(context.Background(), args, stdout, stderr, nil); code != exitUsage ||
				stdout.Len() != 0 || !strings.Contains(stderr.String(), "error=invalid_arguments") || !strings.Contains(stderr.String(), "usage:") {
				t.Fatalf("role %q = %d stdout %q stderr %q", tt.role, code, stdout.String(), stderr.String())
			}
		})
	}
}
