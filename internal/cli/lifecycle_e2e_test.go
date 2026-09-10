package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/installstate"
	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
	"github.com/refactor-ia/cortex/internal/skillroot"
)

// TestCLILifecycleOfflineThreeRuntime is synthetic offline coverage, not version certification.
func TestCLILifecycleOfflineThreeRuntime(t *testing.T) {
	home := t.TempDir()
	install := compatibleInstallDependencies(t, home)
	policy, err := runtimecompat.NewPolicy([]runtimecompat.Entry{
		{ID: runtimematrix.RuntimePi, CertifiedCompatible: []string{"1.2.3"}},
		{ID: runtimematrix.RuntimeOpenCode, CertifiedCompatible: []string{"2.3.4"}},
		{ID: runtimematrix.RuntimeClaudeCode, CertifiedCompatible: []string{"3.4.5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	install.policy = policy

	roots, err := skillroot.ResolveUninstallRoots(skillroot.Inputs{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 3 || roots[0].RuntimeID() != runtimematrix.RuntimePi ||
		roots[1].RuntimeID() != runtimematrix.RuntimeOpenCode || roots[2].RuntimeID() != runtimematrix.RuntimeClaudeCode {
		t.Fatalf("uninstall roots = %#v", roots)
	}
	uninstall := defaultUninstallDependencies()
	uninstall.resolveRoots = func() ([]skillroot.UninstallRoot, error) {
		return append([]skillroot.UninstallRoot(nil), roots...), nil
	}

	runner := readyRunner()
	for path, want := range map[string]string{
		"/private/pi": "1.2.3\n", "/private/opencode": "2.3.4\n", "/private/claude": "3.4.5 (Claude Code)",
	} {
		if got := string(runner.runs[path].execution.Stdout); got != want {
			t.Fatalf("synthetic runtime %s = %q, want %q", path, got, want)
		}
	}

	type installedRuntime struct {
		root      skillroot.UninstallRoot
		sentinel  string
		state     string
		artifacts map[string][]byte
	}
	runtimes := make([]installedRuntime, len(roots))
	for i, root := range roots {
		if err := os.MkdirAll(root.RootPath(), 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(root.RootPath(), "unrelated-sentinel")
		if err := os.WriteFile(sentinel, []byte(root.RuntimeID()+" sentinel bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
		runtimes[i] = installedRuntime{
			root: root, sentinel: sentinel,
			state:     filepath.Join(root.RootPath(), ".cortex", "install-state.json"),
			artifacts: make(map[string][]byte),
		}
	}

	run := func(operation string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := runWithDependencies(context.Background(), []string{operation}, &stdout, &stderr, readyRunner(), install, uninstall)
		if stderr.Len() != 0 {
			t.Fatalf("%s stderr = %q", operation, stderr.String())
		}
		return code, stdout.String()
	}
	assertSentinels := func() {
		t.Helper()
		for _, runtime := range runtimes {
			want := string(runtime.root.RuntimeID()) + " sentinel bytes"
			if got, err := os.ReadFile(runtime.sentinel); err != nil || string(got) != want {
				t.Fatalf("%s sentinel = %q, %v", runtime.root.RuntimeID(), got, err)
			}
		}
	}
	assertOwnedAbsent := func() {
		t.Helper()
		for _, runtime := range runtimes {
			assertMissing(t, runtime.state)
			for path := range runtime.artifacts {
				assertMissing(t, path)
			}
		}
	}
	assertInstalled := func() {
		t.Helper()
		for i := range runtimes {
			runtime := &runtimes[i]
			state, err := os.ReadFile(runtime.state)
			if err != nil {
				t.Fatalf("read %s installed state: %v", runtime.root.RuntimeID(), err)
			}
			manifest, err := installstate.Decode(state)
			if err != nil || manifest.RuntimeID() != runtime.root.RuntimeID() || manifest.RootKind() != runtime.root.RootKind() {
				t.Fatalf("%s installed state = (%#v, %v)", runtime.root.RuntimeID(), manifest, err)
			}
			artifacts := manifest.Artifacts()
			if len(artifacts) != 1 || artifacts[0].LogicalID() != "skills/catalog-marker" {
				t.Fatalf("%s installed artifacts = %#v", runtime.root.RuntimeID(), artifacts)
			}
			path := filepath.Join(runtime.root.RootPath(), artifacts[0].RelativePath())
			contents, err := os.ReadFile(path)
			if err != nil || len(contents) == 0 {
				t.Fatalf("%s installed artifact %s = %q, %v", runtime.root.RuntimeID(), path, contents, err)
			}
			runtime.artifacts[path] = contents
		}
	}
	assertUnchangedArtifacts := func() {
		t.Helper()
		for _, runtime := range runtimes {
			for path, want := range runtime.artifacts {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s updated artifact %s = %q, %v", runtime.root.RuntimeID(), path, got, err)
				}
			}
		}
	}
	expect := func(operation string, wantCode int, wantOutput string) {
		t.Helper()
		if code, got := run(operation); code != wantCode || got != wantOutput {
			t.Fatalf("%s = (%d, %q), want (%d, %q)", operation, code, got, wantCode, wantOutput)
		}
	}

	const compatibleReport = "runtime=pi presence=present compatibility=compatible action=configure touch=denied\n" +
		"runtime=opencode presence=present compatibility=compatible action=configure touch=denied\n" +
		"runtime=claude-code presence=present compatibility=compatible action=configure touch=denied\n"
	expect("doctor", exitOK, compatibleReport)
	assertSentinels()
	assertOwnedAbsent()

	expect("install", exitOK, "operation=install status=completed touch=applied create=6 replace=0 remove=0 unchanged=0 preserve=0\n"+
		"runtime=pi presence=present compatibility=compatible action=configure touch=applied\n"+
		"runtime=opencode presence=present compatibility=compatible action=configure touch=applied\n"+
		"runtime=claude-code presence=present compatibility=compatible action=configure touch=applied\n")
	assertSentinels()
	assertInstalled()

	expect("update", exitOK, "operation=update status=completed touch=applied create=0 replace=0 remove=0 unchanged=6 preserve=0\n"+
		"runtime=pi presence=present compatibility=compatible action=configure touch=applied\n"+
		"runtime=opencode presence=present compatibility=compatible action=configure touch=applied\n"+
		"runtime=claude-code presence=present compatibility=compatible action=configure touch=applied\n")
	assertSentinels()
	assertUnchangedArtifacts()

	expect("uninstall", exitOK, "runtime=pi uninstall=completed remove=2 absent=0 conflict=0\n"+
		"runtime=opencode uninstall=completed remove=2 absent=0 conflict=0\n"+
		"runtime=claude-code uninstall=completed remove=2 absent=0 conflict=0\n")
	assertSentinels()
	assertOwnedAbsent()

	expect("doctor", exitOK, compatibleReport)
	assertSentinels()
	assertOwnedAbsent()
}
