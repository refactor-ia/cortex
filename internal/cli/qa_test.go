package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
		scenario, _ := os.ReadFile("qa-report-scenario")
		if string(bytes.TrimSpace(scenario)) == "model-unavailable" {
			fmt.Print("provider model context max-out thinking images\n")
			return 0
		}
		fmt.Print("provider      model                context  max-out  thinking  images\n" +
			"nan           qwen3.6              262.1K   16.4K    yes       no\n" +
			"nan           glm5.3               262.1K   16.4K    yes       no\n" +
			"openai-codex  gpt-5.6-terra        272.0K   128.0K   yes       yes\n" +
			"nan           deepseek-v4-flash    262.1K   16.4K    yes       no\n")
		return 0
	}
	if os.Args[1] == "auth" {
		scenario, _ := os.ReadFile("qa-report-scenario")
		if string(bytes.TrimSpace(scenario)) == "auth-not-ready" {
			fmt.Print(`{"status":"not_ready","reason":"credential unavailable"}` + "\n")
			return 1
		}
		fmt.Print(`{"status":"ready","provider":"nan","authType":"api_key"}` + "\n")
		return 0
	}
	switch scenario, err := os.ReadFile("qa-report-scenario"); {
	case err != nil:
		return 2
	case string(bytes.TrimSpace(scenario)) == "report" || string(bytes.TrimSpace(scenario)) == "retry-report":
		qaRecordRoleEvidence()
		retrying := string(bytes.TrimSpace(scenario)) == "retry-report"
		if retrying {
			f, err := os.OpenFile("qa-report-invocations", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return 2
			}
			_, _ = f.WriteString("run\n")
			_ = f.Close()
			failed := `{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"error"}`
			fmt.Print("{\"type\":\"agent_start\"}\n{\"type\":\"turn_start\"}\n")
			fmt.Printf("{\"type\":\"message_start\",\"message\":%s}\n{\"type\":\"message_end\",\"message\":%s}\n{\"type\":\"turn_end\",\"message\":%s,\"toolResults\":[]}\n{\"type\":\"agent_end\",\"messages\":[%s],\"willRetry\":true}\n", failed, failed, failed, failed)
			fmt.Print("{\"type\":\"auto_retry_start\",\"attempt\":1,\"maxAttempts\":3,\"delayMs\":2000,\"errorMessage\":\"synthetic failure\"}\n{\"type\":\"entry_appended\",\"entry\":{\"type\":\"context_edit\",\"id\":\"edit\",\"parentId\":\"failed\",\"targetId\":\"failed\",\"timestamp\":\"fixture\",\"replacement\":null}}\n")
		}
		message, _ := json.Marshal(map[string]any{
			"role":     "assistant",
			"content":  []any{map[string]any{"type": "text", "text": fixtureReport}},
			"provider": "nan", "model": "qwen3.6", "stopReason": "stop",
		})
		if !retrying {
			fmt.Print(`{"type":"session","version":3,"id":"fixture","timestamp":"1970-01-01T00:00:00.000Z","cwd":"fixture"}` + "\n")
		}
		fmt.Print(`{"type":"agent_start"}` + "\n" + `{"type":"turn_start"}` + "\n" +
			`{"type":"message_start","message":{"role":"assistant","content":[],"provider":"nan","model":"qwen3.6","stopReason":"pending"}}` + "\n")
		fmt.Printf(`{"type":"message_end","message":%s}`+"\n", message)
		if retrying {
			fmt.Print("{\"type\":\"auto_retry_end\",\"attempt\":1,\"success\":true}\n")
		}
		fmt.Printf(`{"type":"turn_end","message":%s,"toolResults":[]}`+"\n", message)
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
	_ = os.WriteFile("qa-report-frame", frame, 0o600)
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

func TestQARunUnloadableCatalogNamesTheStage(t *testing.T) {
	args := roleReportFixture(t, "requirements-analyst")
	broken := t.TempDir()
	mustOK(os.WriteFile(filepath.Join(broken, "catalog.json"), []byte("not a catalog"), 0o600))
	for index, arg := range args {
		if arg == "--catalog" && index+1 < len(args) {
			args[index+1] = broken
		}
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code == exitOK {
		t.Fatalf("qa run exit = %d, want failure for an unloadable catalog", code)
	}
	if !strings.Contains(stderr.String(), "stage=catalog") {
		t.Fatalf("stderr = %q, want stage=catalog", stderr.String())
	}
}

// TestQARunRefusesAShadowedActorBeforeLaunch pins the public half of #329 and
// #374 S3/S4: a run whose selected actor is shadowed by a project file is
// refused at the admission stage that refused, and the refusal names it instead
// of reporting only that assets were unavailable. Nothing is launched, and the
// disposable install tree and working directory are unchanged afterwards.
func TestQARunRefusesAShadowedActorBeforeLaunch(t *testing.T) {
	args := roleReportFixture(t, qarole.RequirementsAnalyst)
	cwd := must(os.Getwd())
	shadow := filepath.Join(cwd, ".pi", "agents", "other.md")
	mustOK(os.MkdirAll(filepath.Dir(shadow), 0o700))
	mustOK(os.WriteFile(shadow, []byte("---\nname: cortex-requirements-analyst\n---\n"), 0o600))
	before := qaFixtureFingerprint(t, os.Getenv("HOME"), cwd)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), args, stdout, stderr, nil)
	want := "error=actor_unavailable\n" + qaReportNote + "stage=actor_admission\nadmission_stage=actor_shadow_unverified\n"
	if code != exitFailure || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("shadowed run = %d stdout %q stderr %q, want %q", code, stdout.String(), stderr.String(), want)
	}
	for _, artifact := range []string{"qa-report-frame", qaReportRoleEvidencePath, "qa-report-invocations"} {
		if _, err := os.Stat(filepath.Join(cwd, artifact)); !os.IsNotExist(err) {
			t.Fatalf("a shadowed actor wrote %q, want no launch", artifact)
		}
	}
	if after := qaFixtureFingerprint(t, os.Getenv("HOME"), cwd); after != before {
		t.Fatalf("a refused run changed the fixture tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// qaFixtureFingerprint summarizes the disposable roots one refusal may touch —
// relative path, mode, size, and content digest per entry — so a test can prove
// a refused run wrote nothing. It reads only the fixture roots it is given.
func qaFixtureFingerprint(t *testing.T, roots ...string) string {
	t.Helper()
	lines := make([]string, 0, 64)
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			line := fmt.Sprintf("%s %s %d", path, info.Mode(), info.Size())
			if info.Mode().IsRegular() {
				line += fmt.Sprintf(" %x", sha256.Sum256(must(os.ReadFile(path))))
			}
			lines = append(lines, line)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestQARunAcceptsPiInternalRetryWithoutRelaunch(t *testing.T) {
	args := reportFixture(t)
	mustOK(os.WriteFile("qa-report-scenario", []byte("retry-report"), 0o600))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), args, stdout, stderr, nil)
	if code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if calls := string(must(os.ReadFile("qa-report-invocations"))); calls != "run\n" {
		t.Fatalf("backend invocations=%q; want exactly one", calls)
	}
}

func TestQAHelpDocumentsEvidenceSidecarSchema(t *testing.T) {
	stdout := &bytes.Buffer{}
	if code := Run(context.Background(), []string{"qa", "--help"}, stdout, io.Discard, nil); code != exitOK {
		t.Fatalf("qa --help exit = %d", code)
	}
	for _, field := range []string{"id", "command", "exit_code", "output_tail", "provenance", "truncated", "docs/operating.md"} {
		if !strings.Contains(stdout.String(), field) {
			t.Fatalf("qa --help = %q, want the evidence schema field %q documented", stdout.String(), field)
		}
	}
}

func TestQARunHonorsRouteProfileFromCatalog(t *testing.T) {
	args := roleReportFixture(t, "requirements-analyst")
	var catalogRoot string
	for index, arg := range args {
		if arg == "--catalog" && index+1 < len(args) {
			catalogRoot = args[index+1]
		}
	}
	if catalogRoot == "" {
		t.Fatal("fixture did not carry a --catalog argument")
	}
	profileDir := filepath.Join(catalogRoot, "cortex")
	mustOK(os.MkdirAll(profileDir, 0o700))
	profile := []byte(`{"schemaVersion":1,"defaultProfile":"fleet","profiles":{"fleet":{"routes":{"requirements-analyst":{"pi":{"provider":"openai-codex","model":"gpt-5.6-terra","effort":"high"}}}}}}`)
	mustOK(os.WriteFile(filepath.Join(profileDir, "qa-profiles.json"), profile, 0o600))
	t.Cleanup(func() { os.RemoveAll(profileDir) })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK {
		t.Fatalf("qa run exit = %d stderr = %q", code, stderr.String())
	}
	frame := must(os.ReadFile("qa-report-frame"))
	if !bytes.Contains(frame, []byte("route_model gpt-5.6-terra")) {
		t.Fatalf("frame route_model did not come from the catalog profile: %q", frame[:400])
	}
}

// TestQASetupAndRunAgreeOnARelativeCatalog reproduces the issue #324 example
// through the public CLI: setup and run both take the same non-absolute catalog
// path from one directory, so the run must use the profile setup wrote there
// instead of ignoring it. The catalog is a disposable copy.
func TestQASetupAndRunAgreeOnARelativeCatalog(t *testing.T) {
	args := roleReportFixture(t, qarole.RequirementsAnalyst)
	cwd := must(os.Getwd())
	if err := os.CopyFS(filepath.Join(cwd, "catalog"), os.DirFS(qaCatalogArgument(args))); err != nil {
		t.Fatalf("disposable catalog copy = %v", err)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	setup := []string{"qa", "setup", "--catalog", "catalog", "--provider", "nan", "--model", "glm5.3", "--effort", "high"}
	if code := Run(context.Background(), setup, stdout, stderr, nil); code != exitOK {
		t.Fatalf("relative qa setup exit = %d stderr = %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	run := []string{"qa", "run", "--role", "requirements-analyst", "--request", "requirements.txt", "--catalog", "catalog"}
	if code := Run(context.Background(), run, stdout, stderr, nil); code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
		t.Fatalf("relative qa run = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	frame := must(os.ReadFile("qa-report-frame"))
	for _, want := range []string{"route_profile qa-fleet", "route_model glm5.3", "route_backend pi"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Fatalf("relative catalog run ignored the profile setup wrote: frame lost %q", want)
		}
	}
}

// qaSetupCatalogFixture returns a disposable catalog root that already carries
// catalog.json: `qa setup` refuses to create the tree (#324), so a fixture that
// expects a written profile must look like a catalog `qa run` could load.
func qaSetupCatalogFixture(t *testing.T) string {
	t.Helper()
	root := must(filepath.EvalSymlinks(t.TempDir()))
	mustOK(os.WriteFile(filepath.Join(root, "catalog.json"), []byte("{}\n"), 0o600))
	return root
}

func TestQASetupWritesProfileForAllRoles(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	original := qaPiResolver
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = original })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	args := []string{"qa", "setup", "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra", "--effort", "high"}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK {
		t.Fatalf("qa setup exit = %d stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(catalog, "cortex", "qa-profiles.json"))
	if err != nil {
		t.Fatalf("profile file = %v", err)
	}
	var parsed struct {
		SchemaVersion  int    `json:"schemaVersion"`
		DefaultProfile string `json:"defaultProfile"`
		Profiles       map[string]struct {
			Routes map[string]map[string]struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Effort   string `json:"effort"`
			} `json:"routes"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("profile json = %v", err)
	}
	if parsed.SchemaVersion != 1 || parsed.DefaultProfile == "" || len(parsed.Profiles[parsed.DefaultProfile].Routes) != 6 {
		t.Fatalf("profile = %s", data)
	}
	for role, route := range parsed.Profiles[parsed.DefaultProfile].Routes {
		if route["pi"].Provider != "openai-codex" || route["pi"].Model != "gpt-5.6-terra" || route["pi"].Effort != "high" {
			t.Fatalf("role %s route = %+v", role, route["pi"])
		}
	}
}

// refusingPi fails if anything asks it to resolve a runtime binary. qa setup
// must validate the catalog root before it probes model availability (#324),
// so a refusal at the catalog stage proves the probe never ran.
type refusingPi struct{ calls int }

func (resolver *refusingPi) Resolve(context.Context) (string, error) {
	resolver.calls++
	return "", fmt.Errorf("qa setup probed models before validating the catalog root")
}

// TestQASetupRefusesCatalogRootsQaRunCouldNotLoad pins the setup half of the
// source-agreement rule (#324): setup writes the profile only into a catalog
// root `qa run` can load, and it never creates the tree. A typo therefore fails
// loudly instead of writing a profile no run would ever read.
func TestQASetupRefusesCatalogRootsQaRunCouldNotLoad(t *testing.T) {
	root := must(filepath.EvalSymlinks(t.TempDir()))
	mustOK(os.Mkdir(filepath.Join(root, "empty"), 0o700))
	linked := must(filepath.EvalSymlinks(t.TempDir()))
	mustOK(os.WriteFile(filepath.Join(linked, "catalog.json"), []byte("{}\n"), 0o600))
	mustOK(os.Symlink(linked, filepath.Join(root, "linked-catalog")))
	t.Chdir(root)
	for _, testCase := range []struct{ name, catalog, reason string }{
		{name: "absent root", catalog: "typo-catalog", reason: "missing"},
		{name: "directory without catalog.json", catalog: "empty", reason: "catalog_json_missing"},
		{name: "symlinked root", catalog: "linked-catalog", reason: "unusable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resolver := &refusingPi{}
			original := qaPiResolver
			qaPiResolver = resolver
			t.Cleanup(func() { qaPiResolver = original })

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			args := []string{"qa", "setup", "--catalog", testCase.catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra"}
			code := Run(context.Background(), args, stdout, stderr, nil)
			want := "error=setup_failed\nstage=catalog expected=catalog root containing catalog.json catalog_reason=" + testCase.reason + " next_check=cortex qa --help\n"
			if code != exitFailure || stdout.Len() != 0 || stderr.String() != want {
				t.Fatalf("qa setup = %d stdout %q stderr %q, want %q", code, stdout.String(), stderr.String(), want)
			}
			if resolver.calls != 0 {
				t.Fatalf("qa setup resolved the runtime %d times before validating the catalog root", resolver.calls)
			}
			if _, err := os.Stat(filepath.Join(root, testCase.catalog, "cortex")); !os.IsNotExist(err) {
				t.Fatalf("qa setup wrote a profile tree under %q after refusing the catalog root", testCase.catalog)
			}
			if testCase.reason == "missing" {
				if _, err := os.Stat(filepath.Join(root, testCase.catalog)); !os.IsNotExist(err) {
					t.Fatalf("qa setup created %q after refusing the catalog root", testCase.catalog)
				}
			}
		})
	}
}

func TestQASetupRefusesModelsTheRuntimeDoesNotHave(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	original := qaPiResolver
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = original })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	args := []string{"qa", "setup", "--catalog", catalog, "--provider", "openai-codex", "--model", "model-that-does-not-exist"}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure {
		t.Fatalf("qa setup exit = %d, want failure for a model the runtime does not have", code)
	}
	if !strings.Contains(stderr.String(), "gpt-5.6-terra") {
		t.Fatalf("stderr = %q, want the available models listed", stderr.String())
	}
	// Issue #339: a refused pair names why a working provider can still be
	// invisible to qa and how to fix it.
	for _, want := range []string{"--no-extensions", "models.json"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not name %q", stderr.String(), want)
		}
	}
	if _, err := os.Stat(filepath.Join(catalog, "cortex", "qa-profiles.json")); !os.IsNotExist(err) {
		t.Fatal("a refused setup must not write a profile file")
	}
}

func TestQARunEvidenceSidecar(t *testing.T) {
	const evidence = `[{"id":"selected-test","command":"go test -run TestSelected ./example","exit_code":0,"output_tail":"ok example","provenance":"caller fixture","truncated":true}]`
	for _, role := range qarole.Catalog() {
		t.Run(string(role.ID), func(t *testing.T) {
			args := roleReportFixture(t, role.ID)
			mustOK(os.WriteFile("evidence.json", []byte(evidence), 0o600))
			for _, options := range [][]string{
				{"--evidence", "evidence.json"},
				{"--backend", "pi", "--evidence", "evidence.json"},
				{"--evidence", "evidence.json", "--backend", "pi"},
			} {
				stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
				if code := Run(context.Background(), append(args, options...), stdout, stderr, nil); code != exitOK || stderr.Len() != 0 || stdout.String() != fixtureReport+"\n" {
					t.Fatalf("sidecar = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
				}
				frame := must(os.ReadFile("qa-report-frame"))
				if !bytes.Contains(frame, []byte(evidence)) || !bytes.Contains(frame, []byte(fixtureTask)) || !bytes.Contains(frame, []byte("Cite record IDs")) {
					t.Fatal("CLI lost evidence, plain-text task, or attribution instructions")
				}
			}
		})
	}
}

// qaCatalogArgument reads the catalog root out of one assembled qa run argv.
func qaCatalogArgument(args []string) string {
	for index, arg := range args {
		if arg == "--catalog" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

// qaProfileFileFixture writes the user-owned route profile qa run reads: a
// default profile and the qa-fleet profile `cortex qa setup` writes (#374),
// each routing the role to a different model, so which profile a run selected
// is observable in the framed input instead of inferred.
//
// The catalog root is the production catalog the admission binding compares
// against, so any file already there is restored, never left mutated.
func qaProfileFileFixture(t *testing.T, catalogRoot string, role qarole.RoleID) {
	t.Helper()
	profile := fmt.Sprintf(`{"schemaVersion":1,"defaultProfile":"fleet-default","profiles":{"fleet-default":{"routes":{"%s":{"pi":{"provider":"nan","model":"qwen3.6","effort":"low"}}}},"qa-fleet":{"routes":{"%s":{"pi":{"provider":"openai-codex","model":"gpt-5.6-terra","effort":"high"}}}}}}`, role, role)
	qaProfileBodyFixture(t, catalogRoot, profile)
}

// qaProfileBodyFixture stages one exact profile body in the catalog root and
// restores the previous state afterwards. A profile the test itself authored
// is the only way to pin what a diagnostic does with caller-supplied text.
func qaProfileBodyFixture(t *testing.T, catalogRoot, body string) {
	t.Helper()
	profileDirectory := filepath.Join(catalogRoot, "cortex")
	mustOK(os.MkdirAll(profileDirectory, 0o700))
	path := filepath.Join(profileDirectory, "qa-profiles.json")
	previous, previousErr := os.ReadFile(path)
	t.Cleanup(func() {
		if previousErr == nil {
			_ = os.WriteFile(path, previous, 0o600)
			return
		}
		_ = os.Remove(path)
		_ = os.Remove(profileDirectory)
	})
	mustOK(os.WriteFile(path, []byte(body), 0o600))
}

// profileReportFixture materializes the report fixture plus the two-profile
// catalog the CLI resolves --profile against.
func profileReportFixture(t *testing.T) []string {
	t.Helper()
	args := roleReportFixture(t, qarole.RequirementsAnalyst)
	qaProfileFileFixture(t, qaCatalogArgument(args), qarole.RequirementsAnalyst)
	return args
}

// TestQARunProfileFlagSelectsTheRequestedProfile pins the --profile contract
// (#374 S6): the flag selects the named profile instead of the file's
// defaultProfile, and an unknown id is refused instead of silently falling back
// to the default route.
func TestQARunProfileFlagSelectsTheRequestedProfile(t *testing.T) {
	t.Run("an omitted flag keeps the default profile", func(t *testing.T) {
		args := profileReportFixture(t)
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
			t.Fatalf("default profile = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		frame := must(os.ReadFile("qa-report-frame"))
		for _, want := range []string{"route_profile fleet-default", "route_model qwen3.6"} {
			if !bytes.Contains(frame, []byte(want)) {
				t.Fatalf("frame does not carry %q: %q", want, frame)
			}
		}
	})
	t.Run("the flag selects a non-default profile", func(t *testing.T) {
		args := profileReportFixture(t)
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), append(append([]string{}, args...), "--profile", "qa-fleet"), stdout, stderr, nil); code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
			t.Fatalf("--profile qa-fleet = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		frame := must(os.ReadFile("qa-report-frame"))
		for _, want := range []string{"route_profile qa-fleet", "route_model gpt-5.6-terra", "route_effort high"} {
			if !bytes.Contains(frame, []byte(want)) {
				t.Fatalf("frame does not carry %q: %q", want, frame)
			}
		}
	})
	t.Run("an unknown profile id is refused, never silently defaulted", func(t *testing.T) {
		args := profileReportFixture(t)
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), append(append([]string{}, args...), "--profile", "no-such-profile"), stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "error=profile_invalid\n") {
			t.Fatalf("unknown profile = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
		if _, err := os.Stat("qa-report-frame"); !os.IsNotExist(err) {
			t.Fatal("a refused profile must not launch the backend")
		}
	})
}

// TestQARunWithoutAProfileUsesTheDocumentedPlaceholder keeps the legitimate
// default: with no profile source at all, the policy placeholder still routes
// the run (issue #298).
func TestQARunWithoutAProfileUsesTheDocumentedPlaceholder(t *testing.T) {
	args := reportFixture(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
		t.Fatalf("placeholder run = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	frame := must(os.ReadFile("qa-report-frame"))
	for _, want := range []string{"route_profile role-default", "route_model qwen3.6"} {
		if !bytes.Contains(frame, []byte(want)) {
			t.Fatalf("frame does not carry the placeholder %q: %q", want, frame)
		}
	}
}

// TestQARunRefusesAnUnusableDefaultProfileInsteadOfFallingBack pins the other
// half of #324: a profile source that exists but cannot be read safely is a
// named refusal, never a silent placeholder route and never a launch.
func TestQARunRefusesAnUnusableDefaultProfileInsteadOfFallingBack(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		install func(t *testing.T, path string)
	}{
		{name: "symlinked profile file", install: func(t *testing.T, path string) {
			mustOK(os.Symlink(path+".elsewhere", path))
		}},
		{name: "malformed profile file", install: func(t *testing.T, path string) {
			mustOK(os.WriteFile(path, []byte(`{"schemaVersion":1,"defaultProfile":"9bad","profiles":{}}`), 0o600))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			args := roleReportFixture(t, qarole.RequirementsAnalyst)
			// Stage the profile in a disposable copy: the repository catalog
			// must never be mutated by a test that plants an unsafe file.
			catalogRoot := filepath.Join(must(os.Getwd()), "catalog-copy")
			mustOK(os.CopyFS(catalogRoot, os.DirFS(qaCatalogArgument(args))))
			for index, arg := range args {
				if arg == "--catalog" && index+1 < len(args) {
					args[index+1] = catalogRoot
				}
			}
			profileDirectory := filepath.Join(catalogRoot, "cortex")
			mustOK(os.MkdirAll(profileDirectory, 0o700))
			testCase.install(t, filepath.Join(profileDirectory, "qa-profiles.json"))

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run(context.Background(), args, stdout, stderr, nil)
			want := "error=profile_invalid\n" + qaReportNote + "stage=route_resolution\nprofile_source=unusable route_setup=" + filepath.Join(catalogRoot, "cortex", "qa-profiles.json") + "\n"
			if code != exitFailure || stdout.Len() != 0 || stderr.String() != want {
				t.Fatalf("unusable default profile = %d stdout %q stderr %q, want %q", code, stdout.String(), stderr.String(), want)
			}
			if _, err := os.Stat("qa-report-frame"); !os.IsNotExist(err) {
				t.Fatal("a refused default profile must not launch the backend")
			}
		})
	}
}

// TestQARunProfileRefusalNamesTheSelectedProfile pins the refusal half of S6
// (#374): an unknown --profile id names the selection, what the profile source
// actually offers, and the file to edit, without echoing anything unbounded.
func TestQARunProfileRefusalNamesTheSelectedProfile(t *testing.T) {
	args := profileReportFixture(t)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), append(append([]string{}, args...), "--profile", "no-such-profile"), stdout, stderr, nil)
	want := "error=profile_invalid\n" + qaReportNote + "stage=route_resolution\nprofile_selected=no-such-profile profile_source=readable profile_default=fleet-default profile_valid_ids=fleet-default,qa-fleet route_setup=" + filepath.Join(qaCatalogArgument(args), "cortex", "qa-profiles.json") + "\n"
	if code != exitFailure || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("unknown --profile = %d stdout %q stderr %q, want %q", code, stdout.String(), stderr.String(), want)
	}
	// A hostile id is never echoed: the diagnostic is bounded to the profile-id
	// character set and carries no escape or control bytes.
	stdout, stderr = &bytes.Buffer{}, &bytes.Buffer{}
	code = Run(context.Background(), append(append([]string{}, args...), "--profile", "../\x1b[2Jevil"), stdout, stderr, nil)
	if code != exitFailure || strings.Contains(stderr.String(), "\x1b") || !strings.Contains(stderr.String(), "profile_selected=<redacted> ") {
		t.Fatalf("hostile --profile = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

// TestQARunNamesThePreflightRefusalStage pins #374 S4: each preflight refusal
// says which question refused — route resolution, actor admission, model probe,
// or evidence admission — using the stage vocabulary the pipeline already
// distinguishes, and a post-launch outcome claims no preflight stage at all.
func TestQARunNamesThePreflightRefusalStage(t *testing.T) {
	t.Run("route resolution names no resolved route", func(t *testing.T) {
		args := profileReportFixture(t)
		stderr, code := runQARefusal(t, append(append([]string{}, args...), "--profile", "no-such-profile"))
		for _, want := range []string{"stage=route_resolution\n", "profile_valid_ids=fleet-default,qa-fleet"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("unknown --profile stderr %q does not name %q", stderr, want)
			}
		}
		// A refused route resolution never claims a routed pair: none was
		// resolved, so naming one would be a guess (S4/S5).
		if code != exitFailure || strings.Contains(stderr, "route_provider=") || strings.Contains(stderr, "placeholder") {
			t.Fatalf("unknown --profile = %d stderr %q, want no claimed route", code, stderr)
		}
	})
	t.Run("model probe names the routed pair the profile resolved", func(t *testing.T) {
		args := profileReportFixture(t)
		mustOK(os.WriteFile("qa-report-scenario", []byte("model-unavailable"), 0o600))
		stderr, code := runQARefusal(t, append(append([]string{}, args...), "--profile", "qa-fleet"))
		for _, want := range []string{"stage=model_probe\n", "route_provider=openai-codex route_model=gpt-5.6-terra\n"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("model probe stderr %q does not name %q", stderr, want)
			}
		}
		if code != exitFailure {
			t.Fatalf("model probe = %d stderr %q", code, stderr)
		}
	})
	t.Run("an incomplete backend route is a route resolution refusal", func(t *testing.T) {
		args := reportFixture(t)
		catalogRoot := filepath.Join(must(os.Getwd()), "catalog-copy")
		mustOK(os.CopyFS(catalogRoot, os.DirFS(qaCatalogArgument(args))))
		for index, arg := range args {
			if arg == "--catalog" && index+1 < len(args) {
				args[index+1] = catalogRoot
			}
		}
		// The profile routes this role on claude only, so a pi run is
		// incomplete rather than unresolvable.
		qaProfileBodyFixture(t, catalogRoot, `{"schemaVersion":1,"defaultProfile":"fleet","profiles":{"fleet":{"routes":{"requirements-analyst":{"claude":{"provider":"claude","model":"claude-fixture","effort":"medium"}}}}}}`)
		stderr, code := runQARefusal(t, args)
		for _, want := range []string{"error=route_incomplete\n", "stage=route_resolution\n", "route_missing_backend=pi remedy="} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("incomplete route stderr %q does not name %q", stderr, want)
			}
		}
		if code != exitFailure {
			t.Fatalf("incomplete route = %d stderr %q", code, stderr)
		}
	})
	t.Run("evidence admission is named before launch", func(t *testing.T) {
		args := reportFixture(t)
		mustOK(os.WriteFile("evidence.json", []byte(`{"schema":"custom"}`), 0o600))
		stderr, code := runQARefusal(t, append(append([]string{}, args...), "--evidence", "evidence.json"))
		for _, want := range []string{"error=invalid_request\n", "stage=evidence_admission\n"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("evidence refusal stderr %q does not name %q", stderr, want)
			}
		}
		if code != exitFailure {
			t.Fatalf("evidence refusal = %d stderr %q", code, stderr)
		}
		if _, err := os.Stat("qa-report-frame"); !os.IsNotExist(err) {
			t.Fatal("a refused evidence sidecar must not launch the backend")
		}
	})
	t.Run("a post-launch outcome claims no preflight stage", func(t *testing.T) {
		args := reportFixture(t)
		mustOK(os.WriteFile("qa-report-scenario", []byte("nonzero"), 0o600))
		stderr, code := runQARefusal(t, args)
		if code != exitFailure || strings.Contains(stderr, "stage=") {
			t.Fatalf("post-launch failure = %d stderr %q, want no preflight stage", code, stderr)
		}
	})
}

// TestQARunModelProbeEscapesTheRoutedPair pins the safety half of S4/S5: the
// routed provider/model come from a caller-owned profile file, so they are
// bounded and escaped before they reach the operator's terminal. The profile is
// staged in the disposable catalog copy, never in the repository catalog.
func TestQARunModelProbeEscapesTheRoutedPair(t *testing.T) {
	args := reportFixture(t)
	catalogRoot := filepath.Join(must(os.Getwd()), "catalog-copy")
	mustOK(os.CopyFS(catalogRoot, os.DirFS(qaCatalogArgument(args))))
	for index, arg := range args {
		if arg == "--catalog" && index+1 < len(args) {
			args[index+1] = catalogRoot
		}
	}
	qaProfileBodyFixture(t, catalogRoot, `{"schemaVersion":1,"defaultProfile":"fleet-default","profiles":{"fleet-default":{"routes":{"requirements-analyst":{"pi":{"provider":"nan","model":"evil\u001b[2Jmodel","effort":"low"}}}}}}`)
	mustOK(os.WriteFile("qa-report-scenario", []byte("model-unavailable"), 0o600))
	stderr, code := runQARefusal(t, args)
	if code != exitFailure || strings.Contains(stderr, "\x1b") {
		t.Fatalf("hostile routed pair = %d stderr %q, want escaped output", code, stderr)
	}
	if !strings.Contains(stderr, `route_model=evil\x1b[2Jmodel`) {
		t.Fatalf("stderr %q does not escape the routed model", stderr)
	}
}

// TestQAReportNoteClaimsNoRoute pins #374 S5: the prerequisite note printed
// with every non-catalog refusal no longer claims the placeholder "nan" route,
// because a refusal often happens with a profile route already resolved (or
// with no route resolved at all).
func TestQAReportNoteClaimsNoRoute(t *testing.T) {
	for _, banned := range []string{"nan", "placeholder", "default route"} {
		if strings.Contains(qaReportNote, banned) {
			t.Fatalf("the prerequisite note still claims %q: %q", banned, qaReportNote)
		}
	}
}

// runQARefusal runs one public qa invocation that must be refused, and returns
// its stderr and exit code. Success is a test bug: the callers assert on
// refusals only.
func runQARefusal(t *testing.T, args []string) (string, int) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), args, stdout, stderr, nil)
	if code == exitOK {
		t.Fatalf("Run(%q) unexpectedly succeeded: stdout %q", args, stdout.String())
	}
	return stderr.String(), code
}

// TestQARunAcceptsTheDocumentedOptionalFlagsTogether pins the documented
// optional-flag surface (#374 S6): --backend, --evidence and --profile are
// independent and combinable in any order after the required flags.
func TestQARunAcceptsTheDocumentedOptionalFlagsTogether(t *testing.T) {
	const evidence = `[{"id":"selected-test","command":"go test -run TestSelected ./example","exit_code":0,"output_tail":"ok example","provenance":"caller fixture","truncated":false}]`
	cases := []struct {
		name         string
		options      []string
		withEvidence bool
	}{
		{name: "profile alone", options: []string{"--profile", "qa-fleet"}},
		{name: "profile then evidence", options: []string{"--profile", "qa-fleet", "--evidence", "evidence.json"}, withEvidence: true},
		{name: "evidence then profile", options: []string{"--evidence", "evidence.json", "--profile", "qa-fleet"}, withEvidence: true},
		{name: "all three flags", options: []string{"--backend", "pi", "--profile", "qa-fleet", "--evidence", "evidence.json"}, withEvidence: true},
		{name: "all three flags reversed", options: []string{"--evidence", "evidence.json", "--profile", "qa-fleet", "--backend", "pi"}, withEvidence: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			args := profileReportFixture(t)
			mustOK(os.WriteFile("evidence.json", []byte(evidence), 0o600))
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), append(append([]string{}, args...), tt.options...), stdout, stderr, nil); code != exitOK || stdout.String() != fixtureReport+"\n" || stderr.Len() != 0 {
				t.Fatalf("%s = %d stdout %q stderr %q", tt.name, code, stdout.String(), stderr.String())
			}
			frame := must(os.ReadFile("qa-report-frame"))
			wants := []string{"route_profile qa-fleet", "route_model gpt-5.6-terra", "route_backend pi"}
			if tt.withEvidence {
				wants = append(wants, evidence)
			}
			for _, want := range wants {
				if !bytes.Contains(frame, []byte(want)) {
					t.Fatalf("%s lost %q from the frame: %q", tt.name, want, frame)
				}
			}
		})
	}
}

// TestQARunRejectsDuplicateAndUnknownOptionalFlags pins the rejection half of
// the optional-flag surface: duplicates, unknown flags, empty values, more than
// three option pairs, and reordered required flags stay exact usage failures,
// with no backend launch and no route-profile mutation.
func TestQARunRejectsDuplicateAndUnknownOptionalFlags(t *testing.T) {
	const evidenceName = "evidence.json"
	cases := []struct {
		name    string
		options []string
	}{
		{name: "duplicate profile", options: []string{"--profile", "qa-fleet", "--profile", "fleet-default"}},
		{name: "duplicate evidence", options: []string{"--evidence", evidenceName, "--evidence", evidenceName}},
		{name: "duplicate backend", options: []string{"--backend", "pi", "--backend", "pi"}},
		{name: "unknown optional flag", options: []string{"--route", "qa-fleet"}},
		{name: "empty profile value", options: []string{"--profile", ""}},
		{name: "more pairs than optional flags", options: []string{"--profile", "qa-fleet", "--evidence", evidenceName, "--backend", "pi", "--evidence", evidenceName}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			args := profileReportFixture(t)
			mustOK(os.WriteFile(evidenceName, []byte(`[{"id":"selected-test"}]`), 0o600))
			profilePath := filepath.Join(qaCatalogArgument(args), "cortex", "qa-profiles.json")
			before := must(os.ReadFile(profilePath))
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), append(append([]string{}, args...), tt.options...), stdout, stderr, nil); code != exitUsage || stdout.Len() != 0 || stderr.String() != "error=invalid_arguments\n"+qaUsage {
				t.Fatalf("%s = %d stdout %q stderr %q", tt.name, code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat("qa-report-frame"); !os.IsNotExist(err) {
				t.Fatalf("%s launched the backend", tt.name)
			}
			if after := must(os.ReadFile(profilePath)); !bytes.Equal(before, after) {
				t.Fatalf("%s mutated the route profile", tt.name)
			}
		})
	}
	t.Run("required flags keep their order", func(t *testing.T) {
		args := profileReportFixture(t)
		reordered := []string{"qa", "run", "--request", "requirements.txt", "--role", string(qarole.RequirementsAnalyst), "--catalog", qaCatalogArgument(args)}
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), reordered, stdout, stderr, nil); code != exitUsage || stdout.Len() != 0 || stderr.String() != "error=invalid_arguments\n"+qaUsage {
			t.Fatalf("reordered required flags = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
}

func TestQARunRejectsEvidenceInputs(t *testing.T) {
	args := reportFixture(t)
	for _, data := range []string{"", "malformed", `[] []`, strings.Repeat(" ", qapi.MaxEvidenceBytes+1)} {
		mustOK(os.WriteFile("evidence.json", []byte(data), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), append(args, "--evidence", "evidence.json"), stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "error=invalid_request\n") {
			t.Fatalf("invalid evidence = %d stderr %q", code, stderr.String())
		}
	}
	for _, path := range []string{"missing.json", "."} {
		stderr := &bytes.Buffer{}
		if code := Run(context.Background(), append(args, "--evidence", path), io.Discard, stderr, nil); code != exitFailure || !strings.HasPrefix(stderr.String(), "error=invalid_request\n") {
			t.Fatalf("non-file evidence = %d stderr %q", code, stderr.String())
		}
	}
	for _, options := range [][]string{
		{"--evidence"}, {"--evidence", ""},
		{"--evidence", "a", "--evidence", "b"},
		{"--backend", "pi", "--backend", "pi"},
	} {
		if code := Run(context.Background(), append(args, options...), io.Discard, io.Discard, nil); code != exitUsage {
			t.Fatalf("invalid options %q = %d", options, code)
		}
	}
}

func TestQARunEvidenceDiagnosticAttribution(t *testing.T) {
	args := reportFixture(t)
	for _, tc := range []struct {
		name       string
		data       string
		wantField  string
		wantReason string
	}{
		{
			name:       "custom top-level shape with schema",
			data:       `{"schema": "cortex.qa.evidence.v1", "candidate": "cand", "evidence": []}`,
			wantField:  "top-level",
			wantReason: "expected_array",
		},
		{
			name:       "custom top-level shape with diff",
			data:       `{"diff": "...", "pytest": "40 passed"}`,
			wantField:  "top-level",
			wantReason: "expected_array",
		},
		{
			name:       "unknown field in record",
			data:       `[{"id": "test-1", "diff": "line"}]`,
			wantField:  "diff",
			wantReason: "unknown_field",
		},
		{
			name:       "missing id in record",
			data:       `[{"command": "go test"}]`,
			wantField:  "id",
			wantReason: "missing_field",
		},
		{
			name:       "invalid exit_code type",
			data:       `[{"id": "test-1", "exit_code": "0"}]`,
			wantField:  "exit_code",
			wantReason: "invalid_type",
		},
		{
			name:       "unknown field with terminal escape bytes",
			data:       "[{\"id\": \"x\", \"evil\\u001b[31m\\u001b[0m\": 1}]",
			wantField:  "evil??31m??0m",
			wantReason: "unknown_field",
		},
		{
			name:       "duplicate id across records",
			data:       `[{"id": "dup"}, {"id": "dup"}]`,
			wantField:  "id",
			wantReason: "duplicate_id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "evidence.json")
			mustOK(os.WriteFile(path, []byte(tc.data), 0o600))
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			code := Run(context.Background(), append(args, "--evidence", path), stdout, stderr, nil)
			if code != exitFailure {
				t.Fatalf("exit code = %d, want %d", code, exitFailure)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			errOut := stderr.String()
			if !strings.HasPrefix(errOut, "error=invalid_request\n") {
				t.Fatalf("stderr %q lacks error=invalid_request prefix", errOut)
			}
			wantFieldLine := fmt.Sprintf("evidence_field=%s evidence_reason=%s\n", tc.wantField, tc.wantReason)
			if !strings.Contains(errOut, wantFieldLine) {
				t.Fatalf("stderr %q does not contain %q", errOut, wantFieldLine)
			}
		})
	}
}

func TestQAHelp(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "bare root"},
		{name: "root help", args: []string{"help"}},
		{name: "root flag", args: []string{"--help"}},
		{name: "bare qa", args: []string{"qa"}},
		{name: "qa flag", args: []string{"qa", "--help"}},
		{name: "qa run flag", args: []string{"qa", "run", "--help"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), tt.args, stdout, stderr, nil); code != exitOK || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = %d, stdout %q stderr %q", tt.args, code, stdout.String(), stderr.String())
			}
			for _, expected := range []string{
				qaUsage,
				"--role requirements-analyst --request request.txt --catalog ./catalog",
				"catalog.json",
				"plain-text",
				"evaluates evidence",
				"does not run tests",
			} {
				if !strings.Contains(stdout.String(), expected) {
					t.Fatalf("Run(%q) help missing %q: %q", tt.args, expected, stdout.String())
				}
			}
			if !strings.Contains(stdout.String(), "printf '") || len(stdout.String()) > 2048 {
				t.Fatalf("Run(%q) lacks a short request example: %q", tt.args, stdout.String())
			}
		})
	}
}

func TestQAHelpRejectsMalformedInvocations(t *testing.T) {
	for _, args := range [][]string{
		{"help", "extra"}, {"--help", "extra"},
		{"qa", "--help", "extra"}, {"qa", "run", "--help", "extra"},
		{"qa", "run", "--role", "requirements-analyst", "--request", "request.txt", "--catalog", "./catalog", "--help"},
	} {
		t.Run(strings.Join(args, "/"), func(t *testing.T) {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), args, stdout, stderr, nil); code != exitUsage || stdout.Len() != 0 || !strings.Contains(stderr.String(), "error=") {
				t.Fatalf("Run(%q) = %d, stdout %q stderr %q", args, code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestQARunMissingCatalogRoot(t *testing.T) {
	args := reportFixture(t)
	backup := filepath.Join(t.TempDir(), "private-install-backup")
	mustOK(os.Mkdir(backup, 0o700))
	args[len(args)-1] = backup
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 {
		t.Fatalf("missing catalog = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{"error=adapter_unavailable", "stage=catalog", "catalog.json", "cortex qa --help"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("missing catalog guidance %q: %q", expected, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), backup) || strings.Contains(stderr.String(), "private-install-backup") || strings.Contains(stderr.String(), "cortex doctor") || strings.Contains(stderr.String(), "stage=backend") {
		t.Errorf("catalog refusal leaked a path or misidentified the stage: %q", stderr.String())
	}
}

func TestQARunInvalidCatalogManifest(t *testing.T) {
	args := reportFixture(t)
	root := filepath.Join(t.TempDir(), "private-invalid-catalog")
	mustOK(os.Mkdir(root, 0o700))
	mustOK(os.WriteFile(filepath.Join(root, "catalog.json"), []byte("not json"), 0o600))
	args[len(args)-1] = root
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 ||
		!strings.HasPrefix(stderr.String(), "error=adapter_unavailable\nstage=catalog ") ||
		!strings.Contains(stderr.String(), "next_check=cortex qa --help") || strings.Contains(stderr.String(), root) ||
		strings.Contains(stderr.String(), "cortex doctor") {
		t.Fatalf("invalid catalog = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
	}
}

// Issue #339: an unavailable routed model must announce the problem and the
// remedy: a provider registered only by a Pi extension is invisible to qa
// (--no-extensions), and the fix is a core models.json entry. The line names
// the routed pair so the operator can compare it with the core table.
func TestQARunModelUnavailableAnnouncesExtensionInvisibility(t *testing.T) {
	args := reportFixture(t)
	mustOK(os.WriteFile("qa-report-scenario", []byte("model-unavailable"), 0o600))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure {
		t.Fatalf("model unavailable = %d", code)
	}
	for _, want := range []string{"route_provider=nan", "route_model=", "--no-extensions", "models.json"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not name %q", stderr.String(), want)
		}
	}
}

// Issue #339: auth_not_ready must name the actual remedy — complete the
// provider login in an interactive pi session, then rerun — instead of a
// generic prerequisites note.
func TestQARunAuthNotReadyNamesTheLoginRemedy(t *testing.T) {
	args := reportFixture(t)
	mustOK(os.WriteFile("qa-report-scenario", []byte("auth-not-ready"), 0o600))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure {
		t.Fatalf("auth not ready = %d", code)
	}
	for _, want := range []string{"route_provider=nan", "login"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not name %q", stderr.String(), want)
		}
	}
}

func TestQARunFailureStagesRemainDistinct(t *testing.T) {
	args := reportFixture(t)
	for _, tt := range []struct {
		name, scenario, code string
	}{
		{"model unavailable", "model-unavailable", "model_unavailable"},
		{"auth not ready", "auth-not-ready", "auth_not_ready"},
		{"execution failed", "nonzero", "execution_failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mustOK(os.WriteFile("qa-report-scenario", []byte(tt.scenario), 0o600))
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 ||
				!strings.HasPrefix(stderr.String(), "error="+tt.code+"\n") || strings.Contains(stderr.String(), "stage=catalog") ||
				!strings.Contains(stderr.String(), "cortex doctor") {
				t.Fatalf("%s = %d stdout %q stderr %q", tt.name, code, stdout.String(), stderr.String())
			}
		})
	}
	t.Run("unavailable backend binding stays a runtime failure", func(t *testing.T) {
		qaPiResolver = fixturePi{filepath.Join(t.TempDir(), "missing-runtime")}
		defer func() { qaPiResolver = fixturePi{must(os.Executable())} }()
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), args, stdout, stderr, nil); code != exitFailure || stdout.Len() != 0 ||
			!strings.HasPrefix(stderr.String(), "error=unsupported_runtime\n") || strings.Contains(stderr.String(), "stage=catalog") ||
			!strings.Contains(stderr.String(), "cortex doctor") {
			t.Fatalf("unavailable backend = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("unknown backend stays usage failure before catalog loading", func(t *testing.T) {
		args[len(args)-1] = filepath.Join(t.TempDir(), "private-missing-catalog")
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), append(args, "--backend", "unknown"), stdout, stderr, nil); code != exitUsage || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "error=invalid_arguments") || strings.Contains(stderr.String(), "stage=catalog") {
			t.Fatalf("unknown backend = %d stdout %q stderr %q", code, stdout.String(), stderr.String())
		}
	})
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
		got := Run(context.Background(), args, stdout, stderr, nil)
		want := []string{
			"error=normalization_failed",
			"normalization_stage=stream normalization_reason=unterminated",
			"stream_retained=qa-stream-",
		}
		if got != exitFailure || stdout.Len() != 0 {
			t.Fatalf("truncated stream = %d stdout %q stderr %q", got, stdout.String(), stderr.String())
		}
		for _, want := range want {
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
		}
		// Sanitized: no machine paths, no temp dirs, and none of the stream's
		// sensitive fixture content may reach stderr.
		for _, forbidden := range []string{"SENSITIVE-FIXTURE-MARKER", "/private/", t.TempDir()} {
			if strings.Contains(stderr.String(), forbidden) {
				t.Fatalf("stderr = %q, must not contain %q", stderr.String(), forbidden)
			}
		}
	})
	t.Run("invalid arguments exit with usage", func(t *testing.T) {
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), []string{"qa", "run"}, stdout, stderr, nil); code != exitUsage ||
			!strings.Contains(stderr.String(), "error=invalid_arguments") || !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("usage = %d stderr %q", code, stderr.String())
		}
	})
	// An explicit --backend pi must reach exactly the run an omitted flag
	// reaches. The flag selects a backend; it does not open a second path.
	t.Run("explicit pi backend matches the default", func(t *testing.T) {
		mustOK(os.WriteFile("qa-report-scenario", []byte("report"), 0o600))
		stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
		if code := Run(context.Background(), append(append([]string{}, args...), "--backend", "pi"), stdout, stderr, nil); code != exitOK {
			t.Fatalf("explicit pi = %d stderr %q", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Password minimum length is 8 characters") || stderr.Len() != 0 {
			t.Fatalf("explicit pi report = %q stderr %q", stdout.String(), stderr.String())
		}
	})
	// A backend the route policy does not admit is refused as usage, before
	// any catalog, asset or runtime work, and never silently runs on another.
	t.Run("an unadmitted backend is refused with usage", func(t *testing.T) {
		for _, backend := range []string{"codex", "", "PI"} {
			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			if code := Run(context.Background(), append(append([]string{}, args...), "--backend", backend), stdout, stderr, nil); code != exitUsage ||
				stdout.Len() != 0 || !strings.Contains(stderr.String(), "error=invalid_arguments") || !strings.Contains(stderr.String(), "usage:") {
				t.Fatalf("backend %q = %d stdout %q stderr %q", backend, code, stdout.String(), stderr.String())
			}
		}
	})
	// The usage line must name the flag and the admitted set, or a maintainer
	// cannot discover that a second backend is selectable at all.
	t.Run("usage documents the backend flag", func(t *testing.T) {
		for _, expected := range []string{"--backend", "pi", "claude", "opencode"} {
			if !strings.Contains(qaUsage, expected) {
				t.Fatalf("usage omits %q: %q", expected, qaUsage)
			}
		}
	})
	// The prerequisite note must not pin a version on any runtime: decision A
	// removed the exact pin from every binding, so a note demanding one names
	// a prerequisite that does not exist.
	t.Run("the prerequisite note names three backends and pins no version", func(t *testing.T) {
		for _, expected := range []string{"pi", "claude", "opencode", "cortex doctor"} {
			if !strings.Contains(qaReportNote, expected) {
				t.Fatalf("note omits %q: %q", expected, qaReportNote)
			}
		}
		if strings.Contains(qaReportNote, "0.85.1") {
			t.Fatalf("note still pins a runtime version: %q", qaReportNote)
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

// TestQASetupWritesRoutesForTheRequestedBackend pins backend-scoped setup
// (#328): --backend selects which backend the routes are written for, routes
// for other backends already present in the profile are preserved, and an
// unadmitted backend token is a usage error.
func TestQASetupWritesRoutesForTheRequestedBackend(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	original := qaPiResolver
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = original })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	piArgs := []string{"qa", "setup", "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra"}
	if code := Run(context.Background(), piArgs, stdout, stderr, nil); code != exitOK {
		t.Fatalf("pi setup exit = %d stderr = %q", code, stderr.String())
	}
	claudeArgs := []string{"qa", "setup", "--catalog", catalog, "--backend", "claude", "--provider", "claude-bridge", "--model", "claude-fixture"}
	if code := Run(context.Background(), claudeArgs, stdout, stderr, nil); code != exitOK {
		t.Fatalf("claude setup exit = %d stderr = %q", code, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(catalog, "cortex", "qa-profiles.json"))
	if err != nil {
		t.Fatalf("profile file = %v", err)
	}
	var parsed struct {
		Profiles map[string]struct {
			Routes map[string]map[string]struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"routes"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("profile json = %v", err)
	}
	routes := parsed.Profiles["qa-fleet"].Routes
	if len(routes) != 6 {
		t.Fatalf("routes = %d, want 6 roles", len(routes))
	}
	for role, route := range routes {
		if route["pi"].Model != "gpt-5.6-terra" || route["claude"].Model != "claude-fixture" {
			t.Fatalf("role %s routes = %+v; want preserved pi route and claude route", role, route)
		}
	}
	if code := Run(context.Background(), []string{"qa", "setup", "--catalog", catalog, "--backend", "shell", "--provider", "p", "--model", "m"}, io.Discard, io.Discard, nil); code != exitUsage {
		t.Fatalf("unadmitted backend exit = %d, want usage error", code)
	}
}
