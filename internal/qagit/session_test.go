package qagit

import (
	"os"
	"path/filepath"
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
	if plan.Revision != fullSHA1 {
		t.Errorf("Revision = %q, want %q", plan.Revision, fullSHA1)
	}
	if filepath.Dir(plan.Path) != parent {
		t.Errorf("Path = %q, want a child of %q", plan.Path, parent)
	}
	if !strings.HasPrefix(plan.Marker, "session.") {
		t.Errorf("Marker = %q, want a session. prefix", plan.Marker)
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
	plan := SessionPlan{Repository: "/cortex/repo", Revision: fullSHA1, Path: "/cortex/sessions/s1"}
	command := plan.CreateCommand()
	if got := strings.Join(command.Argv, "\n"); got != readGolden(t, "create.argv") {
		t.Errorf("CreateCommand().Argv =\n%s\nwant\n%s", got, readGolden(t, "create.argv"))
	}
	if command.Binary != "git" {
		t.Errorf("Binary = %q, want git", command.Binary)
	}
	if command.CWD != "/cortex/repo" {
		t.Errorf("CWD = %q, want /cortex/repo", command.CWD)
	}
}

func TestPlanSessionCarriesTheRepository(t *testing.T) {
	parent, repository := ownedParent(t), ownedParent(t)
	plan, err := PlanSession(SessionRequest{Repository: repository, Revision: fullSHA1, Parent: parent, SessionID: "s1"})
	if err != nil {
		t.Fatalf("PlanSession() error = %v, want nil", err)
	}
	if plan.Repository != repository {
		t.Errorf("Repository = %q, want %q", plan.Repository, repository)
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
