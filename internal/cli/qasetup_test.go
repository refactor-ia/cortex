package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
)

func runQASetupFixture(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	original := qaPiResolver
	qaPiResolver = fixturePi{must(os.Executable())}
	t.Cleanup(func() { qaPiResolver = original })
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), append([]string{"qa", "setup"}, args...), stdout, stderr, nil)
	return code, stdout.String(), stderr.String()
}

func readQAProfileRoutes(t *testing.T, catalog string) map[string]map[string]qaProfileRoute {
	t.Helper()
	var parsed qaProfileFile
	mustOK(json.Unmarshal(must(os.ReadFile(filepath.Join(catalog, "cortex", "qa-profiles.json"))), &parsed))
	return parsed.Profiles[parsed.DefaultProfile].Routes
}

// S3: --role writes only that role's route for the selected backend and
// preserves every other role and backend route.
func TestQASetupRoleWritesOnlyThatRole(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	if code, _, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra"); code != exitOK {
		t.Fatalf("base setup = %d %q", code, stderr)
	}
	if code, _, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "anthropic", "--model", "opus", "--backend", "claude"); code != exitOK {
		t.Fatalf("claude setup = %d %q", code, stderr)
	}
	before := readQAProfileRoutes(t, catalog)
	target := string(qarole.Catalog()[0].ID)
	code, stdout, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "anthropic", "--model", "sonnet", "--backend", "claude", "--effort", "low", "--role", target)
	if code != exitOK || stderr != "" {
		t.Fatalf("role setup = %d stdout %q stderr %q", code, stdout, stderr)
	}
	want := "qa route profile written to " + filepath.Join(catalog, "cortex", "qa-profiles.json") + " (role " + target + " -> claude: anthropic/sonnet effort=low)\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	after := readQAProfileRoutes(t, catalog)
	if len(after) != len(before) {
		t.Fatalf("roles = %d, want %d", len(after), len(before))
	}
	for role, byBackend := range before {
		for backend, route := range byBackend {
			expected := route
			if role == target && backend == "claude" {
				expected = qaProfileRoute{Provider: "anthropic", Model: "sonnet", Effort: "low"}
			}
			if after[role][backend] != expected {
				t.Fatalf("route %s/%s = %+v, want %+v", role, backend, after[role][backend], expected)
			}
		}
	}
}

// S3: --role on an empty catalog writes just that role.
func TestQASetupRoleOnFreshCatalogWritesOneRole(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	target := string(qarole.Catalog()[1].ID)
	if code, _, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra", "--role", target); code != exitOK {
		t.Fatalf("role setup = %d %q", code, stderr)
	}
	routes := readQAProfileRoutes(t, catalog)
	if len(routes) != 1 || routes[target]["pi"] != (qaProfileRoute{Provider: "openai-codex", Model: "gpt-5.6-terra", Effort: "high"}) {
		t.Fatalf("routes = %+v", routes)
	}
}

// S3: an unknown role is refused, valid ids listed, file unchanged.
func TestQASetupRefusesUnknownRole(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	if code, _, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra"); code != exitOK {
		t.Fatalf("base setup = %d %q", code, stderr)
	}
	path := filepath.Join(catalog, "cortex", "qa-profiles.json")
	before := must(os.ReadFile(path))
	code, stdout, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra", "--role", "no-such-role")
	if code != exitUsage || stdout != "" {
		t.Fatalf("unknown role = %d stdout %q", code, stdout)
	}
	ids := make([]string, 0, len(qarole.Catalog()))
	for _, contract := range qarole.Catalog() {
		ids = append(ids, string(contract.ID))
	}
	want := "error=unknown_role\nrole=no-such-role\nvalid roles:\n" + strings.Join(ids, "\n") + "\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
	if after := must(os.ReadFile(path)); !bytes.Equal(before, after) {
		t.Fatalf("profile changed after refusal:\n%s", after)
	}
}

// S3: without --role setup still rewrites every role identically.
func TestQASetupWithoutRoleRewritesAllRoles(t *testing.T) {
	catalog := qaSetupCatalogFixture(t)
	target := string(qarole.Catalog()[0].ID)
	if code, _, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra", "--role", target); code != exitOK {
		t.Fatalf("role setup = %d %q", code, stderr)
	}
	code, stdout, stderr := runQASetupFixture(t, "--catalog", catalog, "--provider", "openai-codex", "--model", "gpt-5.6-terra", "--effort", "low")
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "(6 roles -> pi: openai-codex/gpt-5.6-terra effort=low)") {
		t.Fatalf("setup = %d stdout %q stderr %q", code, stdout, stderr)
	}
	routes := readQAProfileRoutes(t, catalog)
	if len(routes) != len(qarole.Catalog()) {
		t.Fatalf("routes = %+v", routes)
	}
	for role, byBackend := range routes {
		if byBackend["pi"] != (qaProfileRoute{Provider: "openai-codex", Model: "gpt-5.6-terra", Effort: "low"}) {
			t.Fatalf("role %s = %+v", role, byBackend)
		}
	}
}
