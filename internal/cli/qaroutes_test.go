package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/qarole"
)

func qaRoutesCatalog(t *testing.T, profile string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if profile != "" {
		if err := os.MkdirAll(filepath.Join(root, "cortex"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "cortex", "qa-profiles.json"), []byte(profile), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const qaRoutesProfile = `{"schemaVersion":1,"defaultProfile":"mine","profiles":{"mine":{"routes":{"test-runner":{"claude":{"provider":"acme","model":"m1","effort":"low"}}}}}}`

func TestQARoutesListsProfileRoutes(t *testing.T) {
	root := qaRoutesCatalog(t, qaRoutesProfile)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"qa", "routes", "--catalog", root, "--profile", "mine"}, stdout, stderr, nil); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != len(qarole.Catalog())*3 {
		t.Fatalf("got %d lines:\n%s", len(lines), stdout)
	}
	if lines[0] != "role=requirements-analyst backend=pi provider=- model=- effort=- source=unset" {
		t.Fatalf("first line %q", lines[0])
	}
	want := "role=test-runner backend=claude provider=acme model=m1 effort=low source=profile:mine"
	if !strings.Contains(stdout.String(), want+"\n") {
		t.Fatalf("missing %q in:\n%s", want, stdout)
	}
}

func TestQARoutesWithoutProfileReportsPolicyDefault(t *testing.T) {
	root := qaRoutesCatalog(t, "")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"qa", "routes", "--catalog", root}, stdout, stderr, nil); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != len(qarole.Catalog())*3 || !strings.HasPrefix(lines[0], "role=requirements-analyst backend=pi ") {
		t.Fatalf("output:\n%s", stdout)
	}
	for _, line := range lines {
		if !strings.HasSuffix(line, " source=role-default") && !strings.HasSuffix(line, " source=unset") {
			t.Fatalf("unexpected source in %q", line)
		}
	}
}

func TestQARoutesInvalidProfileMatchesRunDiagnostic(t *testing.T) {
	root := qaRoutesCatalog(t, qaRoutesProfile)
	before, _ := os.ReadFile(filepath.Join(root, "cortex", "qa-profiles.json"))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(context.Background(), []string{"qa", "routes", "--catalog", root, "--profile", "other"}, stdout, stderr, nil)
	want := "error=profile_invalid\n" + qaReportNote + "stage=route_resolution\n" + qaProfileDiagnostic("other", root)
	if code != exitFailure || stdout.Len() != 0 || stderr.String() != want {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after, _ := os.ReadFile(filepath.Join(root, "cortex", "qa-profiles.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("profile changed")
	}
}

func TestQARoutesRejectsBadArguments(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := Run(context.Background(), []string{"qa", "routes"}, stdout, stderr, nil); code != exitUsage || stderr.String() != "error=invalid_arguments\n"+qaUsage {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
