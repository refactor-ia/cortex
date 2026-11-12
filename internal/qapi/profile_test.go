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

func TestProfileRouteHonorsDefaultProfileWithoutFlag(t *testing.T) {
	root := testProfileRoot(t)
	data := []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"evidence-auditor":{"pi":{"provider":"nan","model":"glm5.3","effort":"high"}}}}}}`)
	writeProfile(t, root, data)

	got, failure := resolveProfileRoute(root, AdmissionRequest{
		Role: qarole.EvidenceAuditor, Backend: "pi",
	})
	if failure.Code != "" || got.Model != "glm5.3" || got.Provider != "nan" || got.ProfileID != "balanced" {
		t.Fatalf("resolveProfileRoute() = (%+v, %q), want the file's defaultProfile route", got, failure.Code)
	}
}

func TestProfileRouteDefaultBypassesProfileSource(t *testing.T) {
	got, failure := resolveProfileRoute(filepath.Join(t.TempDir(), "missing"), AdmissionRequest{
		Role: qarole.ExploratoryTester, Backend: "pi",
	})
	if failure.Code != "" || got.Model != "glm5.3" || got.ProfileID != "role-default" {
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

const validProfileSnapshot = `{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"test-runner":{"pi":{"provider":"nan","model":"qwen3.6","effort":"low"}}}}}}`

// profileSourceCase is one profile source that exists but cannot be inspected
// safely. An explicit --profile selection and a default run must refuse the
// same shapes, so both tests below share one table (#324).
type profileSourceCase struct {
	name  string
	setup func(t *testing.T, root string)
}

func unusableProfileSources(valid []byte) []profileSourceCase {
	return []profileSourceCase{
		{"symlink root", func(t *testing.T, root string) {
			target := testProfileRoot(t)
			writeProfile(t, target, valid)
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, root); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink parent", func(t *testing.T, root string) {
			target := t.TempDir()
			writeProfile(t, target, valid)
			if err := os.Symlink(filepath.Join(target, profileDirectory), filepath.Join(root, profileDirectory)); err != nil {
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
		{"default profile id not admitted", func(t *testing.T, root string) {
			writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"9bad","profiles":{}}`))
		}},
	}
}

func TestProfileRouteRejectsInvalidSources(t *testing.T) {
	valid := []byte(validProfileSnapshot)
	cases := append([]profileSourceCase{{"missing", func(t *testing.T, root string) {}}}, unusableProfileSources(valid)...)
	for _, tc := range cases {
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

// TestProfileRouteRejectsUnusableDefaultProfile pins the fail-closed half of
// the default-profile rule (#324): a profile source that exists but cannot be
// read safely is a refusal, never a silent fallback to the policy placeholder.
func TestProfileRouteRejectsUnusableDefaultProfile(t *testing.T) {
	for _, tc := range unusableProfileSources([]byte(validProfileSnapshot)) {
		t.Run(tc.name, func(t *testing.T) {
			root := testProfileRoot(t)
			tc.setup(t, root)
			got, failure := resolveProfileRoute(root, AdmissionRequest{Role: qarole.TestRunner, Backend: "pi"})
			if failure.Code != "profile_invalid" || got.PolicyVersion != "" {
				t.Fatalf("resolveProfileRoute() = (%+v, %q), want profile_invalid instead of the placeholder fallback", got, failure.Code)
			}
		})
	}
}

// TestProfileRouteDefaultPlaceholderRequiresAnAbsentSource keeps the
// legitimate half: with no profile source at all, the policy placeholder still
// applies (issue #298).
func TestProfileRouteDefaultPlaceholderRequiresAnAbsentSource(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"no profile directory", func(t *testing.T, root string) {}},
		{"profile directory without a file", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, profileDirectory), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testProfileRoot(t)
			tc.setup(t, root)
			got, failure := resolveProfileRoute(root, AdmissionRequest{Role: qarole.TestRunner, Backend: "pi"})
			if failure.Code != "" || got.ProfileID != "role-default" || got.Model != "qwen3.6" {
				t.Fatalf("resolveProfileRoute() = (%+v, %q), want the placeholder route", got, failure.Code)
			}
		})
	}
}

func TestProfileRouteHonorsProfileDefinedModels(t *testing.T) {
	// A profile-covered role resolves with the profile's own provider/model
	// even when they differ from the placeholder defaults (issue #298:
	// each owner routes with their own). Roles a profile does not cover
	// still fail with route_incomplete — no silent default fallback.
	root := testProfileRoot(t)
	writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"balanced","profiles":{"balanced":{"routes":{"exploratory-tester":{"pi":{"provider":"nan","model":"qwen3.6","effort":"high"}}}}}}`))

	got, failure := resolveProfileRoute(root, AdmissionRequest{Role: qarole.ExploratoryTester, Backend: "pi", Profile: "balanced"})
	if failure.Code != "" || got.Model != "qwen3.6" || got.ProfileID != "balanced" {
		t.Fatalf("resolveProfileRoute() = (%+v, %q), want the profile-defined route", got, failure.Code)
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

// TestProfileSelectionReportsTheSameSnapshotResolutionReads pins the S6 (#374)
// listing seam: the default id and the sorted admitted ids come from the one
// safe snapshot read the route policy uses, so a refusal enumerates ids the
// policy itself accepts instead of a second, weaker representation.
func TestProfileSelectionReportsTheSameSnapshotResolutionReads(t *testing.T) {
	root := testProfileRoot(t)
	writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"fleet","profiles":{"qa-fleet":{"routes":{"test-runner":{"pi":{"provider":"nan","model":"qwen3.6","effort":"low"}}}},"fleet":{"routes":{"test-runner":{"pi":{"provider":"nan","model":"glm5.3","effort":"high"}}}}}}`))
	defaultID, ids, ok := ProfileSelection(root)
	if !ok || defaultID != "fleet" {
		t.Fatalf("ProfileSelection() = (%q, %q, %v), want the file's defaultProfile", defaultID, ids, ok)
	}
	if len(ids) != 2 || ids[0] != "fleet" || ids[1] != "qa-fleet" {
		t.Fatalf("ProfileSelection() ids = %q, want [fleet qa-fleet]", ids)
	}
	// An absent or unsafe source has no selection at all: a caller reports the
	// refusal instead of listing ids from a file it could not verify.
	for name, setup := range map[string]func(t *testing.T, root string){
		"absent": func(t *testing.T, root string) {},
		"symlink": func(t *testing.T, root string) {
			mustSymlink(t, filepath.Join(testProfileRoot(t), "elsewhere.json"), stagedProfilePath(t, root))
		},
		"malformed": func(t *testing.T, root string) {
			writeProfile(t, root, []byte(`{"schemaVersion":1,"defaultProfile":"9bad","profiles":{}}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := testProfileRoot(t)
			setup(t, root)
			if defaultID, ids, ok := ProfileSelection(root); ok || defaultID != "" || ids != nil {
				t.Fatalf("ProfileSelection() = (%q, %q, %v), want no selection", defaultID, ids, ok)
			}
		})
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// stagedProfilePath creates the profile directory of root and returns the file
// path inside it, without writing a file there.
func stagedProfilePath(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(profilePath(root)), 0o755); err != nil {
		t.Fatal(err)
	}
	return profilePath(root)
}
