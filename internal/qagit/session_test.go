package qagit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	fullSHA1   = "0123456789abcdef0123456789abcdef01234567"
	fullSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func ownedParent(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	return resolved
}

func TestPlanSessionAcceptsAFullRevisionAndACanonicalTarget(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if plan.Revision() != fullSHA1 {
		t.Errorf("Revision = %q, want %q", plan.Revision(), fullSHA1)
	}
	if filepath.Dir(plan.Path()) != parent {
		t.Errorf("Path = %q, want a child of %q", plan.Path(), parent)
	}
	if !strings.HasPrefix(plan.Marker(), "session.") {
		t.Errorf("Marker = %q, want a session. prefix", plan.Marker())
	}
}

func TestPlanSessionExposesReadOnlyPlanData(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if plan.Repository() != repository {
		t.Errorf("Repository() = %q, want %q", plan.Repository(), repository)
	}
	if plan.Revision() != fullSHA1 {
		t.Errorf("Revision() = %q, want %q", plan.Revision(), fullSHA1)
	}
	if plan.Path() != filepath.Join(parent, "s1") {
		t.Errorf("Path() = %q, want %q", plan.Path(), filepath.Join(parent, "s1"))
	}
	if plan.Marker() != sessionMarker(fullSHA1, plan.Path()) {
		t.Errorf("Marker() = %q, want the plan marker", plan.Marker())
	}
}

func TestPlanSessionRefusesEveryNonImmutableRevision(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	for name, revision := range map[string]string{
		"branch name":  "main",
		"tag":          "v1.2.3",
		"short sha":    fullSHA1[:12],
		"symbolic ref": "HEAD",
		"peeled ref":   "HEAD^{commit}",
		"empty":        "",
		"uppercase":    strings.ToUpper(fullSHA1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PlanSession(SessionRequest{Repository: repository, Revision: revision, Parent: parent, SessionID: "s1"})
			if !isSessionFailure(err, SessionRevisionNotImmutable) {
				t.Errorf("PlanSession(%q) error = %v, want %v", revision, err, SessionRevisionNotImmutable)
			}
		})
	}
}

func TestPlanSessionAcceptsASHA256Revision(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	if _, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA256, Parent: parent, SessionID: "s1"}); err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
}

func TestPlanSessionRefusesAnUnusableParent(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)

	occupied := filepath.Join(parent, "taken")
	if err := os.Mkdir(occupied, 0o755); err != nil {
		t.Fatalf("create occupied directory: %v", err)
	}
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(occupied, linked); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	for name, request := range map[string]SessionRequest{
		"relative parent":   {Repository: repository, Revision: fullSHA1, Parent: "relative/path", SessionID: "s1"},
		"traversal parent":  {Repository: repository, Revision: fullSHA1, Parent: parent + string(filepath.Separator) + "..", SessionID: "s1"},
		"symlinked parent":  {Repository: repository, Revision: fullSHA1, Parent: linked, SessionID: "s1"},
		"missing parent":    {Repository: repository, Revision: fullSHA1, Parent: filepath.Join(parent, "absent"), SessionID: "s1"},
		"file as parent":    {Repository: repository, Revision: fullSHA1, Parent: writeFile(t, parent), SessionID: "s1"},
		"empty parent":      {Repository: repository, Revision: fullSHA1, Parent: "", SessionID: "s1"},
		"occupied target":   {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "taken"},
		"empty session":     {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: ""},
		"traversal session": {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: ".."},
		"separator session": {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "a/b"},
		"dotfile session":   {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: ".hidden"},
		"uppercase session": {Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "S1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PlanSession(request)
			if !isSessionFailure(err, SessionTargetNotOwnable) {
				t.Errorf("PlanSession() error = %v, want %v", err, SessionTargetNotOwnable)
			}
		})
	}
}

func writeFile(t *testing.T, parent string) string {
	t.Helper()
	path := filepath.Join(parent, "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func isSessionFailure(err error, code SessionFailureCode) bool {
	failure, ok := err.(SessionFailure)
	return ok && failure.Code == code
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "session-plan", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return strings.TrimSuffix(string(content), "\n")
}

func goldenSessionPlan(t *testing.T) SessionPlan {
	t.Helper()
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	return plan
}

func goldenArgv(t *testing.T, name string, plan SessionPlan) string {
	t.Helper()
	return strings.NewReplacer(
		"/cortex/repo", plan.Repository(),
		"/cortex/sessions/s1", plan.Path(),
	).Replace(readGolden(t, name))
}

func TestSessionMarkerMatchesItsIndependentGolden(t *testing.T) {
	marker := sessionMarker(fullSHA1, "/cortex/sessions/s1")
	if want := readGolden(t, "marker.txt"); marker != want {
		t.Errorf("sessionMarker() = %q, want %q", marker, want)
	}
}

func TestSessionMarkerSeparatesItsFields(t *testing.T) {
	// Length framing must make these distinct: concatenating the fields
	// differently produces the same bytes only if the frames are missing.
	if sessionMarker("ab", "c") == sessionMarker("a", "bc") {
		t.Error("sessionMarker() collides across differently split fields")
	}
	if sessionMarker(fullSHA1, "/a") == sessionMarker(fullSHA1, "/b") {
		t.Error("sessionMarker() ignores the path")
	}
	if sessionMarker(fullSHA1, "/a") == sessionMarker(fullSHA256, "/a") {
		t.Error("sessionMarker() ignores the revision")
	}
}

func TestCreateCommandMatchesItsGoldenArgv(t *testing.T) {
	plan := goldenSessionPlan(t)
	command, err := plan.CreateCommand()
	if err != nil {
		t.Fatalf("CreateCommand() error = %v, want nil", err)
	}
	if got := strings.Join(command.Argv, "\n"); got != goldenArgv(t, "create.argv", plan) {
		t.Errorf("CreateCommand().Argv =\n%s\nwant\n%s", got, goldenArgv(t, "create.argv", plan))
	}
	if command.Binary != "git" {
		t.Errorf("Binary = %q, want git", command.Binary)
	}
	if command.CWD != plan.Repository() {
		t.Errorf("CWD = %q, want %q", command.CWD, plan.Repository())
	}
}

func TestPlanSessionCarriesTheRepository(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if plan.Repository() != repository {
		t.Errorf("Repository() = %q, want %q", plan.Repository(), repository)
	}
}

func TestPlanSessionRefusesAnUnusableRepository(t *testing.T) {
	parent := ownedParent(t)
	for name, repository := range map[string]string{
		"empty":     "",
		"relative":  "relative/path",
		"traversal": parent + string(filepath.Separator) + "..",
		"missing":   filepath.Join(parent, "absent"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
			if !isSessionFailure(err, SessionTargetNotOwnable) {
				t.Errorf("PlanSession() error = %v, want %v", err, SessionTargetNotOwnable)
			}
		})
	}
}

func TestRemoveCommandMatchesItsGoldenArgv(t *testing.T) {
	plan := goldenSessionPlan(t)
	command, err := plan.RemoveCommand()
	if err != nil {
		t.Fatalf("RemoveCommand() error = %v, want nil", err)
	}
	if got := strings.Join(command.Argv, "\n"); got != goldenArgv(t, "remove.argv", plan) {
		t.Errorf("RemoveCommand().Argv =\n%s\nwant\n%s", got, goldenArgv(t, "remove.argv", plan))
	}
}

func TestCommandsAreTheClosedWorktreeSet(t *testing.T) {
	plan := goldenSessionPlan(t)
	commands, err := plan.Commands()
	if err != nil {
		t.Fatalf("Commands() error = %v, want nil", err)
	}
	if len(commands) != 2 {
		t.Fatalf("Commands() returned %d commands, want 2", len(commands))
	}
	for index, command := range commands {
		if command.Binary != "git" {
			t.Errorf("Commands()[%d].Binary = %q, want git", index, command.Binary)
		}
		// Every command must address exactly this repository and speak only
		// worktree. No other Git subcommand can be expressed at all.
		if len(command.Argv) < 4 || command.Argv[0] != "-C" || command.Argv[1] != plan.Repository() || command.Argv[2] != "worktree" {
			t.Fatalf("Commands()[%d].Argv = %v, want -C <repository> worktree ...", index, command.Argv)
		}
		switch verb := command.Argv[3]; verb {
		case "add", "remove":
		default:
			t.Errorf("Commands()[%d] uses worktree %q, which is outside the closed set", index, verb)
		}
		if !slices.Contains(command.Argv, "--") {
			t.Errorf("Commands()[%d].Argv = %v, want an end-of-options marker", index, command.Argv)
		}
	}
}

func TestPlanningWritesNothing(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	if err := os.WriteFile(filepath.Join(repository, "existing"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seed repository: %v", err)
	}
	before := treeSnapshot(t, parent, repository)

	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if _, err := plan.CreateCommand(); err != nil {
		t.Fatalf("CreateCommand() error = %v, want nil", err)
	}
	if _, err := plan.RemoveCommand(); err != nil {
		t.Fatalf("RemoveCommand() error = %v, want nil", err)
	}
	if _, err := plan.Commands(); err != nil {
		t.Fatalf("Commands() error = %v, want nil", err)
	}

	if after := treeSnapshot(t, parent, repository); after != before {
		t.Errorf("planning changed the filesystem:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Lstat(plan.Path()); !os.IsNotExist(err) {
		t.Errorf("planning created %q", plan.Path())
	}
}

func TestRemoveCommandAllowsAnExistingTarget(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if err := os.Mkdir(plan.Path(), 0o755); err != nil {
		t.Fatalf("create planned target: %v", err)
	}
	if _, err := plan.RemoveCommand(); err != nil {
		t.Fatalf("RemoveCommand() error = %v after target creation, want nil", err)
	}
}

func TestSessionPlanCommandMethodsRejectInvalidPlans(t *testing.T) {
	valid := goldenSessionPlan(t)
	modifiedRevision := valid
	modifiedRevision.revision = "main"
	modifiedPath := valid
	modifiedPath.path = "/cortex/sessions/other"
	plans := map[string]SessionPlan{
		"zero value":        {},
		"fabricated":        {repository: valid.repository, revision: valid.revision, path: valid.path, marker: valid.marker},
		"modified revision": modifiedRevision,
		"modified path":     modifiedPath,
	}

	for name, plan := range plans {
		t.Run(name, func(t *testing.T) {
			command, err := plan.CreateCommand()
			if !isSessionFailure(err, SessionPlanInvalid) {
				t.Errorf("CreateCommand() error = %v, want %v", err, SessionPlanInvalid)
			}
			if command.Binary != "" || len(command.Argv) != 0 {
				t.Errorf("CreateCommand() = %#v, want an unusable command", command)
			}

			command, err = plan.RemoveCommand()
			if !isSessionFailure(err, SessionPlanInvalid) {
				t.Errorf("RemoveCommand() error = %v, want %v", err, SessionPlanInvalid)
			}
			if command.Binary != "" || len(command.Argv) != 0 {
				t.Errorf("RemoveCommand() = %#v, want an unusable command", command)
			}

			commands, err := plan.Commands()
			if !isSessionFailure(err, SessionPlanInvalid) {
				t.Errorf("Commands() error = %v, want %v", err, SessionPlanInvalid)
			}
			if len(commands) != 0 {
				t.Errorf("Commands() = %#v, want no usable commands", commands)
			}
		})
	}
}

// treeSnapshot records every entry under the given roots with its mode and
// size, so any creation, removal, or rewrite changes the result.
func treeSnapshot(t *testing.T, roots ...string) string {
	t.Helper()
	var entries []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			entries = append(entries, fmt.Sprintf("%s %s %d", path, info.Mode(), info.Size()))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	slices.Sort(entries)
	return strings.Join(entries, "\n")
}
