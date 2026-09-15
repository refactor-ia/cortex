package qagit

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	results []Result
	errs    []error
	calls   []Command
}

func (f *fakeRunner) Run(_ context.Context, command Command) (Result, error) {
	f.calls = append(f.calls, command)
	i := len(f.calls) - 1
	if i < len(f.errs) {
		return f.results[i], f.errs[i]
	}
	return f.results[i], nil
}

func TestVerifyCleanBindingAcceptsFullOIDFormats(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			head := fixture(t, format+"-head.oid")
			tree := fixture(t, format+"-tree.oid")
			cwd := canonicalTempDir(t)
			request := Request{CWD: cwd, Revision: head, Fingerprint: candidateFingerprint(format, head, tree)}
			runner := &fakeRunner{results: responses(cwd, format, head, tree, "")}
			binding, err := VerifyCleanBinding(context.Background(), request, runner)
			if err != nil {
				t.Fatal(err)
			}
			if binding.ObjectFormat != format || binding.Revision != head || binding.Tree != tree || binding.Fingerprint != request.Fingerprint {
				t.Fatalf("unexpected binding: %#v", binding)
			}
			assertCommands(t, runner.calls, cwd)
		})
	}
}

func TestVerifyCleanBindingAddsCWDPathIdentity(t *testing.T) {
	root := canonicalTempDir(t)
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	request := Request{CWD: root, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}

	binding, err := VerifyCleanBinding(context.Background(), request, &fakeRunner{results: responses(root, "sha1", head, tree, "")})
	if err != nil {
		t.Fatal(err)
	}
	if want := cwdIdentityOracle(root); binding.CWDIdentity != want {
		t.Fatalf("CWDIdentity = %q, want independent framed oracle %q", binding.CWDIdentity, want)
	}
}

func TestVerifyCleanBindingCWDPathIdentityDiffersAcrossCanonicalRoots(t *testing.T) {
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	identities := make([]string, 0, 2)
	for _, root := range []string{canonicalTempDir(t), canonicalTempDir(t)} {
		request := Request{CWD: root, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}
		binding, err := VerifyCleanBinding(context.Background(), request, &fakeRunner{results: responses(root, "sha1", head, tree, "")})
		if err != nil {
			t.Fatal(err)
		}
		identities = append(identities, binding.CWDIdentity)
	}
	if identities[0] == identities[1] {
		t.Fatalf("canonical roots produced the same CWD identity: %q", identities[0])
	}
}

func TestVerifyCleanBindingRejectsPathIdentityWithoutCanonicalGitRoot(t *testing.T) {
	root, head, tree := canonicalTempDir(t), strings.Repeat("a", 40), strings.Repeat("b", 40)
	link := filepath.Join(t.TempDir(), "candidate")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	otherRoot := canonicalTempDir(t)
	for _, test := range []struct {
		name    string
		request Request
		results []Result
	}{
		{"symlink", Request{CWD: link, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}, nil},
		{"noncanonical", Request{CWD: root + "/.", Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}, nil},
		{"git top-level mismatch", Request{CWD: root, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}, responses(otherRoot, "sha1", head, tree, "")},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding, err := VerifyCleanBinding(context.Background(), test.request, &fakeRunner{results: test.results})
			if err == nil {
				t.Fatal("invalid path identity was accepted")
			}
			if binding != (Binding{}) || binding.CWDIdentity != "" {
				t.Fatalf("failure returned a binding with CWD identity: %#v", binding)
			}
		})
	}
}

// cwdIdentityOracle independently assembles the exact two-frame wire form:
// 8-byte big-endian domain byte length, domain bytes, 8-byte big-endian root
// byte length, then the canonical Git worktree-root path bytes.
func cwdIdentityOracle(root string) string {
	frames := [][]byte{[]byte("cortex.qa.cwd.v1"), []byte(root)}
	wire := make([]byte, 0, len(frames[0])+len(frames[1])+16)
	for _, frame := range frames {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(frame)))
		wire = append(wire, length[:]...)
		wire = append(wire, frame...)
	}
	return fmt.Sprintf("cwd.%x", sha256.Sum256(wire))
}

func TestVerifyCleanBindingRejectsUnsafeRequests(t *testing.T) {
	cwd := canonicalTempDir(t)
	for _, request := range []Request{
		{CWD: "relative", Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64)},
		{CWD: cwd + "/.", Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64)},
		{CWD: cwd, Revision: "HEAD", Fingerprint: "candidate." + strings.Repeat("b", 64)},
		{CWD: cwd, Revision: strings.Repeat("A", 40), Fingerprint: "candidate." + strings.Repeat("b", 64)},
		{CWD: cwd, Revision: strings.Repeat("a", 12), Fingerprint: "candidate." + strings.Repeat("b", 64)},
	} {
		runner := &fakeRunner{}
		if _, err := VerifyCleanBinding(context.Background(), request, runner); err == nil || len(runner.calls) != 0 {
			t.Fatalf("unsafe request was accepted: %#v", request)
		}
	}
}

func TestVerifyCleanBindingRejectsSymlinkedDirectory(t *testing.T) {
	cwd := canonicalTempDir(t)
	link := filepath.Join(t.TempDir(), "candidate")
	if err := os.Symlink(cwd, link); err != nil {
		t.Fatal(err)
	}
	request := Request{CWD: link, Revision: strings.Repeat("a", 40), Fingerprint: "candidate." + strings.Repeat("b", 64)}
	if _, err := VerifyCleanBinding(context.Background(), request, &fakeRunner{}); err == nil {
		t.Fatal("symlinked directory was accepted")
	}
}

func TestVerifyCleanBindingRejectsStaleAndDirtyEvidence(t *testing.T) {
	cwd, head, tree := canonicalTempDir(t), strings.Repeat("a", 40), strings.Repeat("b", 40)
	request := Request{CWD: cwd, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}
	for _, results := range [][]Result{
		responses(cwd, "sha1", strings.Repeat("c", 40), tree, ""),
		responses(cwd, "sha1", head, tree, "1 M. N... 100644 100644 100644 a b file\x00"),
		responses(cwd, "sha1", head, tree, "? file\x00"),
	} {
		runner := &fakeRunner{results: results}
		if _, err := VerifyCleanBinding(context.Background(), request, runner); err == nil {
			t.Fatal("unsafe evidence was accepted")
		}
	}
	request.Fingerprint = "candidate." + strings.Repeat("0", 64)
	if _, err := VerifyCleanBinding(context.Background(), request, &fakeRunner{results: responses(cwd, "sha1", head, tree, "")}); err == nil {
		t.Fatal("stale fingerprint was accepted")
	}
}

func TestVerifyCleanBindingRejectsEveryCommandFailure(t *testing.T) {
	cwd, head, tree := canonicalTempDir(t), strings.Repeat("a", 40), strings.Repeat("b", 40)
	request := Request{CWD: cwd, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}
	for failure := range 6 {
		runner := &fakeRunner{results: responses(cwd, "sha1", head, tree, ""), errs: make([]error, 6)}
		runner.results[failure].ExitCode = 1
		if _, err := VerifyCleanBinding(context.Background(), request, runner); err == nil || len(runner.calls) != failure+1 {
			t.Fatalf("command %d failure was accepted", failure)
		}
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Join("testdata", "clean-binding", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(bytes))
}

func responses(cwd, format, head, tree, status string) []Result {
	return []Result{{Stdout: []byte(cwd + "\n")}, {Stdout: []byte(cwd + "\n")}, {Stdout: []byte(format + "\n")}, {Stdout: []byte(head + "\n")}, {Stdout: []byte(tree + "\n")}, {Stdout: []byte(status)}}
}

func TestVerifyCleanBindingAcceptsNormalCheckoutWithAbsoluteCommonDir(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a disposable Git checkout")
	}
	cwd := canonicalTempDir(t)
	runFixtureGit(t, cwd, "init", "--quiet")
	runFixtureGit(t, cwd, "config", "user.email", "fixture@example.invalid")
	runFixtureGit(t, cwd, "config", "user.name", "Fixture")
	runFixtureGit(t, cwd, "config", "commit.gpgsign", "false")
	runFixtureGit(t, cwd, "config", "core.hooksPath", "/dev/null")
	runFixtureGit(t, cwd, "commit", "--allow-empty", "--quiet", "-m", "fixture")

	commonDir := runFixtureGit(t, cwd, "rev-parse", "--git-common-dir")
	if filepath.IsAbs(commonDir) {
		t.Fatalf("normal checkout common directory = %q, want relative", commonDir)
	}
	head := runFixtureGit(t, cwd, "rev-parse", "--verify", "HEAD^{commit}")
	tree := runFixtureGit(t, cwd, "rev-parse", "--verify", "HEAD^{tree}")
	request := Request{CWD: cwd, Revision: head, Fingerprint: candidateFingerprint("sha1", head, tree)}

	binding, err := VerifyCleanBinding(context.Background(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ObjectFormat != "sha1" || binding.Revision != head || binding.Tree != tree || binding.Fingerprint != request.Fingerprint {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func runFixtureGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	command.Env = gitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func assertCommands(t *testing.T, calls []Command, cwd string) {
	t.Helper()
	want := [][]string{{"-C", cwd, "rev-parse", "--show-toplevel"}, {"-C", cwd, "rev-parse", "--path-format=absolute", "--git-common-dir"}, {"-C", cwd, "rev-parse", "--show-object-format"}, {"-C", cwd, "rev-parse", "--verify", "HEAD^{commit}"}, {"-C", cwd, "rev-parse", "--verify", "HEAD^{tree}"}, {"-C", cwd, "status", "--porcelain=v2", "-z", "--untracked-files=all"}}
	if len(calls) != len(want) {
		t.Fatalf("calls = %d", len(calls))
	}
	for i := range want {
		if calls[i].CWD != cwd || strings.Join(calls[i].Argv, "\x00") != strings.Join(want[i], "\x00") || len(calls[i].Environment) == 0 {
			t.Fatalf("command %d = %#v", i, calls[i])
		}
	}
}
