package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestModelProfileApplyRollbackAndNoOp(t *testing.T) {
	home, pi := profileTempDir(t), profileTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	settings := filepath.Join(pi, "settings.json")
	subagents := filepath.Join(pi, "subagents.json")
	putProfileFile(t, settings, `{"defaultProvider":"old","defaultModel":"old","defaultThinkingLevel":"high"}`, 0o640)
	putProfileFile(t, subagents, `{"model_profiles":{"sdd-proposal":{"model":"old","effort":"high"}}}`, 0o600)

	runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "pi"}, 0)
	nanSettings, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	nanMode := fileMode(t, settings)
	runProfile(t, []string{"model-routing", "profile", "apply", "openai", "--runtime", "pi", "--scope", "user"}, 0)
	runProfile(t, []string{"model-routing", "profile", "rollback", "--scope", "user", "--runtime", "pi"}, 0)
	got, err := os.ReadFile(settings)
	if err != nil || !bytes.Equal(got, nanSettings) || fileMode(t, settings) != nanMode {
		t.Fatalf("rollback = %q mode %o, want %q mode %o (%v)", got, fileMode(t, settings), nanSettings, nanMode, err)
	}
	pointer := filepath.Join(home, ".cortex", "model-profile", "current")
	before, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "pi"}, 0)
	after, err := os.ReadFile(pointer)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("no-op replaced rollback pointer: %q -> %q (%v)", before, after, err)
	}
}

func TestModelProfileRejectsDriftAndUnsupportedOpenCodeOverride(t *testing.T) {
	home, pi := profileTempDir(t), profileTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	settings := filepath.Join(pi, "settings.json")
	putProfileFile(t, settings, `{"defaultProvider":"old","defaultModel":"old"}`, 0o600)
	putProfileFile(t, filepath.Join(pi, "subagents.json"), `{}`, 0o600)
	runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "pi"}, 0)
	putProfileFile(t, settings, `{"defaultProvider":"drift","defaultModel":"drift"}`, 0o600)
	var stdout, stderr bytes.Buffer
	if got := Run(context.Background(), []string{"model-routing", "profile", "rollback", "--scope", "user"}, &stdout, &stderr, nil); got == 0 || stderr.String() != "error=model_profile_rollback_failed\n" {
		t.Fatalf("drift rollback = %d stdout %q stderr %q", got, stdout.String(), stderr.String())
	}
	openRoot := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(openRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	putProfileFile(t, filepath.Join(openRoot, "opencode.json"), `{"agent":{}}`, 0o600)
	putProfileFile(t, filepath.Join(openRoot, "opencode.jsonc"), `{}`, 0o600)
	stdout.Reset()
	stderr.Reset()
	if got := Run(context.Background(), []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "opencode"}, &stdout, &stderr, nil); got == 0 || stderr.String() != "error=model_profile_unsupported_opencode_override\n" {
		t.Fatalf("override apply = %d stdout %q stderr %q", got, stdout.String(), stderr.String())
	}
}

func runProfile(t *testing.T, args []string, want int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if got := Run(context.Background(), args, &stdout, &stderr, nil); got != want {
		t.Fatalf("Run(%q) = %d, want %d; stdout %q stderr %q", args, got, want, stdout.String(), stderr.String())
	}
}

func profileTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func putProfileFile(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
