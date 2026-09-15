package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "--model") {
		os.Exit(fakePi())
	}
	os.Exit(m.Run())
}

// fakePi is a deterministic fake Pi subprocess used only as test evidence; it
// never performs provider, authentication, or network work.
func fakePi() int {
	if os.Args[1] == "--version" {
		fmt.Println(qapi.RuntimeVersion)
		return 0
	}
	switch scenario, err := os.ReadFile("qa-report-scenario"); {
	case err != nil:
		return 2
	case string(bytes.TrimSpace(scenario)) == "report":
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

func (fixture fixturePi) ResolvePi(context.Context) (string, error) { return fixture.path, nil }

// reportFixture materializes one installed requirements-analyst role from the
// production catalog and points the command at it with the fake Pi binary.
func reportFixture(t *testing.T) []string {
	t.Helper()
	snapshot := must(catalog.BuildCatalogSnapshot("../../catalog", "catalog.json", catalog.AdmissionPolicy{}))
	expected := must(qapi.CatalogAdmissionBinding(snapshot, qarole.RequirementsAnalyst, "pi"))
	sources := must(qaactor.Sources(snapshot))
	binding := must(qaactor.Bind(must(qaactor.ProjectPi(must(qaactor.Render(sources))))))
	neutral := must(skillrender.Render(snapshot))
	skills := must(skillprojection.Build(runtimematrix.RuntimePi, neutral))
	var actor qaactor.ProjectedActor
	var skill skillprojection.ProjectedSkill
	for _, candidate := range binding.Actors() {
		if candidate.RoleID() == qarole.RequirementsAnalyst {
			actor = candidate
		}
	}
	for _, candidate := range skills.Skills() {
		if candidate.CapabilityID() == "requirements-analyst" && candidate.LogicalID() == "skills/requirements-analyst" {
			skill = candidate
		}
	}
	root := must(filepath.EvalSymlinks(t.TempDir()))
	cwd := must(filepath.EvalSymlinks(t.TempDir()))
	installRoot := filepath.Join(root, ".pi", "agent")
	const installation = installstate.InstallationID("000102030405060708090a0b0c0d0e0f")
	inputs := []installstate.V2ArtifactInput{{LogicalID: "skills/requirements-analyst", Kind: installstate.KindSkill, CapabilityID: "requirements-analyst", RelativePath: "skills/cortex-requirements-analyst/SKILL.md", SHA256: expected.SkillSHA256, InstallationID: installation}}
	for _, role := range qarole.Catalog() {
		actorHash := strings.Repeat("a", 64)
		if role.ID == qarole.RequirementsAnalyst {
			actorHash = expected.ActorSHA256
		}
		inputs = append(inputs, installstate.V2ArtifactInput{LogicalID: "actors/" + string(role.ID), Kind: installstate.KindPiActor, RoleID: role.ID, ActorContractVersion: qaactor.ActorContractVersion, RelativePath: "agents/cortex-" + string(role.ID) + ".md", SHA256: actorHash, InstallationID: installation})
	}
	state := must(installstate.NewV2(runtimematrix.RuntimePi, skilldest.RootKindPiUserAgent, snapshot.Fingerprint(), installation, inputs))
	mustOK(os.MkdirAll(filepath.Join(installRoot, ".cortex"), 0o700))
	mustOK(os.MkdirAll(filepath.Join(installRoot, "agents"), 0o700))
	mustOK(os.MkdirAll(filepath.Join(installRoot, "skills", "cortex-requirements-analyst"), 0o700))
	mustOK(os.WriteFile(filepath.Join(installRoot, ".cortex", "install-state.json"), must(installstate.Encode(state)), 0o600))
	mustOK(os.WriteFile(filepath.Join(installRoot, "agents", "cortex-requirements-analyst.md"), actor.Content(), 0o600))
	mustOK(os.WriteFile(filepath.Join(installRoot, "skills", "cortex-requirements-analyst", "SKILL.md"), skill.Content(), 0o600))
	mustOK(os.WriteFile(filepath.Join(cwd, "requirements.txt"), []byte(fixtureTask), 0o600))
	mustOK(os.WriteFile(filepath.Join(cwd, "qa-report-scenario"), []byte("report"), 0o600))
	catalogRoot := must(filepath.Abs("../../catalog"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("HOME", root)
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = nil })
	t.Chdir(cwd)
	return []string{"qa", "run", "--role", "requirements-analyst", "--request", "requirements.txt", "--catalog", catalogRoot}
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
