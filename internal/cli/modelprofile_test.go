package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/refactor-ia/cortex/internal/modelprofile"
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

func TestModelProfileAllLeavesCyclePreservesExactImages(t *testing.T) {
	home, pi := profileTempDir(t), profileTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	t.Setenv("OPENCODE_CONFIG", "")
	settings := filepath.Join(pi, "settings.json")
	subagents := filepath.Join(pi, "subagents.json")
	openConfig := filepath.Join(home, ".config", "opencode", "opencode.json")
	originalSettings := `{"defaultProvider":"old","defaultModel":"old","defaultThinkingLevel":"high"}`
	originalSubagents := `{"model_profiles":{"sdd-proposal":{"model":"old"}}}`
	originalOpen := `{"agent":{"gentle-orchestrator":{"mode":"primary"}}}`
	putProfileFile(t, settings, originalSettings, 0o640)
	putProfileFile(t, subagents, originalSubagents, 0o600)
	putProfileFile(t, openConfig, originalOpen, 0o644)
	runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user"}, exitOK)
	runProfile(t, []string{"model-routing", "profile", "rollback", "--scope", "user"}, exitOK)
	wantProfileFile(t, settings, originalSettings, 0o640)
	wantProfileFile(t, subagents, originalSubagents, 0o600)
	wantProfileFile(t, openConfig, originalOpen, 0o644)
}

func TestModelProfileSaveRejectsTransformInputDrift(t *testing.T) {
	home, pi := profileTempDir(t), profileTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	t.Setenv("OPENCODE_CONFIG", "")
	settings := filepath.Join(pi, "settings.json")
	subagents := filepath.Join(pi, "subagents.json")
	originalSettings := `{"defaultProvider":"old","defaultModel":"old","defaultThinkingLevel":"high","unrelated":"A"}`
	originalSubagents := `{}`
	putProfileFile(t, settings, originalSettings, 0o640)
	putProfileFile(t, subagents, originalSubagents, 0o600)
	roots := modelprofile.RuntimeRoots{Pi: pi}
	changes, err := profileChanges(roots, modelprofile.Profile("nan"), "pi")
	if err != nil || len(changes) != 1 {
		t.Fatalf("profileChanges = %d changes, %v", len(changes), err)
	}
	foreignSettings := `{"defaultProvider":"old","defaultModel":"old","defaultThinkingLevel":"high","unrelated":"B"}`
	putProfileFile(t, settings, foreignSettings, 0o644)
	store := profileTempDir(t)
	if err := modelprofile.Save(store, roots, changes); err == nil {
		t.Fatal("Save accepted a transform based on stale settings")
	}
	wantProfileFile(t, settings, foreignSettings, 0o644)
	wantProfileFile(t, subagents, originalSubagents, 0o600)
}

func TestModelProfileInterventionErrorCode(t *testing.T) {
	if got := profileErrorCode(modelprofile.ErrInterventionRequired, "model_profile_apply_failed"); got != "model_profile_intervention_required" {
		t.Fatalf("profileErrorCode(intervention) = %q", got)
	}
}

func TestModelProfileStatusReportsExecutionFailure(t *testing.T) {
	home, pi := profileTempDir(t), profileTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	var stdout, stderr bytes.Buffer
	if got := Run(context.Background(), []string{"model-routing", "profile", "status"}, &stdout, &stderr, nil); got != exitFailure || stderr.String() != "error=model_profile_status_failed\n" {
		t.Fatalf("status = %d stdout %q stderr %q", got, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".cortex")); !os.IsNotExist(err) {
		t.Fatalf("status wrote profile state: %v", err)
	}
}

func TestModelProfileExplicitRuntimePreflightIsolated(t *testing.T) {
	t.Run("opencode ignores incomplete Pi", func(t *testing.T) {
		home, pi := profileTempDir(t), profileTempDir(t)
		t.Setenv("HOME", home)
		t.Setenv("PI_CODING_AGENT_DIR", pi)
		t.Setenv("OPENCODE_CONFIG", "")
		settings := filepath.Join(pi, "settings.json")
		putProfileFile(t, settings, `{"defaultProvider":"old","defaultModel":"old"}`, 0o600)
		openConfig := filepath.Join(home, ".config", "opencode", "opencode.json")
		originalOpen := `{"agent":{"gentle-orchestrator":{"mode":"primary"}}}`
		putProfileFile(t, openConfig, originalOpen, 0o600)
		runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "opencode"}, exitOK)
		wantProfileFile(t, settings, `{"defaultProvider":"old","defaultModel":"old"}`, 0o600)
		if got, _ := os.ReadFile(openConfig); bytes.Equal(got, []byte(originalOpen)) {
			t.Fatal("OpenCode config was not applied")
		}
	})
	t.Run("pi ignores OpenCode override", func(t *testing.T) {
		home, pi := profileTempDir(t), profileTempDir(t)
		t.Setenv("HOME", home)
		t.Setenv("PI_CODING_AGENT_DIR", pi)
		t.Setenv("OPENCODE_CONFIG", "external")
		settings := filepath.Join(pi, "settings.json")
		putProfileFile(t, settings, `{"defaultProvider":"old","defaultModel":"old"}`, 0o600)
		putProfileFile(t, filepath.Join(pi, "subagents.json"), `{}`, 0o600)
		openJSONC := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
		putProfileFile(t, openJSONC, `{}`, 0o600)
		runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "pi"}, exitOK)
		if got, _ := os.ReadFile(settings); bytes.Equal(got, []byte(`{"defaultProvider":"old","defaultModel":"old"}`)) {
			t.Fatal("Pi settings were not applied")
		}
		wantProfileFile(t, openJSONC, `{}`, 0o600)
	})
	t.Run("selected invalid runtime rejects without writes", func(t *testing.T) {
		home, pi := profileTempDir(t), profileTempDir(t)
		t.Setenv("HOME", home)
		t.Setenv("PI_CODING_AGENT_DIR", pi)
		settings := filepath.Join(pi, "settings.json")
		original := `{"defaultProvider":"old","defaultModel":"old"}`
		putProfileFile(t, settings, original, 0o600)
		runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "pi"}, exitFailure)
		wantProfileFile(t, settings, original, 0o600)
		if _, err := os.Stat(filepath.Join(home, ".cortex")); !os.IsNotExist(err) {
			t.Fatalf("invalid selected Pi wrote profile state: %v", err)
		}
		t.Setenv("OPENCODE_CONFIG", "external")
		openConfig := filepath.Join(home, ".config", "opencode", "opencode.json")
		originalOpen := `{"agent":{"gentle-orchestrator":{"mode":"primary"}}}`
		putProfileFile(t, openConfig, originalOpen, 0o600)
		runProfile(t, []string{"model-routing", "profile", "apply", "nan", "--scope", "user", "--runtime", "opencode"}, exitFailure)
		wantProfileFile(t, openConfig, originalOpen, 0o600)
	})
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

func wantProfileFile(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != data || fileMode(t, path) != mode {
		t.Fatalf("%s = %q mode %o, want %q mode %o (%v)", path, got, fileMode(t, path), data, mode, err)
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
