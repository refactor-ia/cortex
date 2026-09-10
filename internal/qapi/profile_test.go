package qapi

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/refactor-ia/cortex/internal/qaadmission"
	"github.com/refactor-ia/cortex/internal/qarole"
)

func TestProfileRouteDefaultBypassesProfileSource(t *testing.T) {
	got, failure := resolveProfileRoute(filepath.Join(t.TempDir(), "missing"), AdmissionRequest{
		Role: qarole.ExploratoryTester, Backend: "pi",
	})
	if failure.Code != "" || got.Model != "glm5.2" || got.ProfileID != "role-default" {
		t.Fatalf("resolveProfileRoute() = (%+v, %q)", got, failure.Code)
	}
}

func TestProfileRouteUsesOneOriginalSnapshot(t *testing.T) {
	root := testProfileRoot(t)
	data := []byte("\n {\"schemaVersion\":1,\"defaultProfile\":\"balanced\",\"profiles\":{\"balanced\":{\"routes\":{\"test-runner\":{\"pi\":{\"provider\":\"nan\",\"model\":\"qwen3.6\",\"effort\":\"low\"}}}}}} \n")
	writeProfile(t, root, data)

	got, failure := resolveProfileRoute(root, AdmissionRequest{
		Role: qarole.TestRunner, Backend: "pi", Profile: "balanced",
	})
	if failure.Code != "" {
		t.Fatalf("resolveProfileRoute() failure = %q", failure.Code)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(data))
	if got.ProfileSHA256 != wantDigest || got.ProfileID != "balanced" || got.Provider != "nan" || got.Model != "qwen3.6" || got.Effort != "low" {
		t.Fatalf("resolveProfileRoute() = %+v", got)
	}
}

func TestProfileRouteRejectsInvalidSources(t *testing.T) {
	valid := []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"test-runner":{"pi":{"provider":"nan","model":"qwen3.6","effort":"low"}}}}}}`)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"missing", func(t *testing.T, root string) {}},
		{"symlink root", func(t *testing.T, root string) {
			target := testProfileRoot(t)
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink parent", func(t *testing.T, root string) {
			target := t.TempDir()
			if err := os.Symlink(target, filepath.Join(root, "cortex")); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink leaf", func(t *testing.T, root string) {
			writeProfile(t, root, valid)
			path := profilePath(root)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "elsewhere"), path); err != nil {
				t.Fatal(err)
			}
		}},
		{"nonregular directory", func(t *testing.T, root string) {
			if err := os.MkdirAll(profilePath(root), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"nonregular fifo", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Dir(profilePath(root)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(profilePath(root), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversized", func(t *testing.T, root string) {
			writeProfile(t, root, []byte(strings.Repeat(" ", qaadmission.MaxProfileSnapshotBytes+1)))
		}},
		{"malformed", func(t *testing.T, root string) { writeProfile(t, root, []byte("{")) }},
		{"duplicate", func(t *testing.T, root string) {
			writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{}},"balanced":{"routes":{}}}}`))
		}},
		{"trailing", func(t *testing.T, root string) { writeProfile(t, root, append(valid, []byte(" {}")...)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testProfileRoot(t)
			tc.setup(t, root)
			got, failure := resolveProfileRoute(root, AdmissionRequest{Role: qarole.TestRunner, Backend: "pi", Profile: "balanced"})
			if failure.Code != "profile_invalid" || got.PolicyVersion != "" {
				t.Fatalf("resolveProfileRoute() = (%+v, %q), want profile_invalid", got, failure.Code)
			}
		})
	}
}

func TestProfileRoutePreservesRouteFailuresWithoutDefaults(t *testing.T) {
	root := testProfileRoot(t)
	writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"exploratory-tester":{"pi":{"provider":"nan","model":"qwen3.6","effort":"high"}}}}}}`))

	got, failure := resolveProfileRoute(root, AdmissionRequest{Role: qarole.ExploratoryTester, Backend: "pi", Profile: "balanced"})
	if failure.Code != "route_disallowed" || got.PolicyVersion != "" {
		t.Fatalf("resolveProfileRoute() = (%+v, %q), want route_disallowed without glm5.2 fallback", got, failure.Code)
	}
}

func testProfileRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func profilePath(root string) string {
	return filepath.Join(root, "cortex", "qa-profiles.json")
}

func writeProfile(t *testing.T, root string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(profilePath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath(root), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
