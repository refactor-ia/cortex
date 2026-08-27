package projection

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/refactor-ia/cortex/internal/catalog"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

const testFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestResultValues(t *testing.T) {
	if Exact != "exact" || Translated != "translated" || Unrepresentable != "unrepresentable" {
		t.Fatal("projection result values must match the runtime adapter contract")
	}
}

func TestNewAssessment(t *testing.T) {
	tests := []struct {
		name       string
		runtimeID  runtimematrix.RuntimeID
		result     Result
		disclosure string
	}{
		{"pi exact", runtimematrix.RuntimePi, Exact, ""},
		{"pi translated", runtimematrix.RuntimePi, Translated, "equivalent representation"},
		{"pi unrepresentable", runtimematrix.RuntimePi, Unrepresentable, ""},
		{"opencode exact", runtimematrix.RuntimeOpenCode, Exact, ""},
		{"opencode translated", runtimematrix.RuntimeOpenCode, Translated, "equivalent representation"},
		{"opencode unrepresentable", runtimematrix.RuntimeOpenCode, Unrepresentable, ""},
		{"claude code exact", runtimematrix.RuntimeClaudeCode, Exact, ""},
		{"claude code translated", runtimematrix.RuntimeClaudeCode, Translated, "equivalent representation"},
		{"claude code unrepresentable", runtimematrix.RuntimeClaudeCode, Unrepresentable, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment, err := NewAssessment(test.runtimeID, testFingerprint, test.result, test.disclosure)
			if err != nil {
				t.Fatalf("NewAssessment() error = %v", err)
			}
			if assessment.RuntimeID() != test.runtimeID || assessment.SnapshotFingerprint() != testFingerprint || assessment.Result() != test.result || assessment.TranslationDisclosure() != test.disclosure {
				t.Fatal("assessment accessors did not preserve validated values")
			}
		})
	}
}

func TestNewAssessmentValidation(t *testing.T) {
	tests := []struct {
		name        string
		runtimeID   runtimematrix.RuntimeID
		fingerprint string
		result      Result
		disclosure  string
		secret      string
	}{
		{"unsupported runtime", "unknown", testFingerprint, Exact, "", "unknown"},
		{"short fingerprint", runtimematrix.RuntimePi, "abc", Exact, "", "abc"},
		{"uppercase fingerprint", runtimematrix.RuntimePi, strings.ToUpper(testFingerprint), Exact, "", strings.ToUpper(testFingerprint)},
		{"non hexadecimal fingerprint", runtimematrix.RuntimePi, strings.Repeat("g", 64), Exact, "", strings.Repeat("g", 64)},
		{"unknown result", runtimematrix.RuntimePi, testFingerprint, "other", "", "other"},
		{"exact disclosure", runtimematrix.RuntimePi, testFingerprint, Exact, "equivalent", "equivalent"},
		{"translated missing disclosure", runtimematrix.RuntimePi, testFingerprint, Translated, "", ""},
		{"translated untrimmed disclosure", runtimematrix.RuntimePi, testFingerprint, Translated, " equivalent ", " equivalent "},
		{"unrepresentable disclosure", runtimematrix.RuntimePi, testFingerprint, Unrepresentable, "reason", "reason"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment, err := NewAssessment(test.runtimeID, test.fingerprint, test.result, test.disclosure)
			if assessment != (Assessment{}) || err == nil {
				t.Fatal("NewAssessment() must return a zero assessment and error")
			}
			if test.secret != "" && strings.Contains(err.Error(), test.secret) {
				t.Fatal("NewAssessment() error leaked invalid input")
			}
		})
	}
}

func TestNewAssessmentIsDeterministicAndImmutable(t *testing.T) {
	disclosure := "equivalent representation"
	first, err := NewAssessment(runtimematrix.RuntimePi, testFingerprint, Translated, disclosure)
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	disclosure = "changed"
	second, err := NewAssessment(runtimematrix.RuntimePi, testFingerprint, Translated, "equivalent representation")
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	if first != second || first.TranslationDisclosure() != "equivalent representation" {
		t.Fatal("assessment must preserve immutable scalar values")
	}
}

func TestValidateBinding(t *testing.T) {
	snapshot := testSnapshot(t)
	assessment, err := NewAssessment(runtimematrix.RuntimePi, snapshot.Fingerprint(), Exact, "")
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	if err := ValidateBinding(snapshot, runtimematrix.RuntimePi, assessment); err != nil {
		t.Fatalf("ValidateBinding() error = %v", err)
	}

	otherRuntime, err := NewAssessment(runtimematrix.RuntimeOpenCode, snapshot.Fingerprint(), Exact, "")
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	otherSnapshot, err := NewAssessment(runtimematrix.RuntimePi, strings.Repeat("a", 64), Exact, "")
	if err != nil {
		t.Fatalf("NewAssessment() error = %v", err)
	}
	for _, test := range []struct {
		name       string
		runtime    runtimematrix.RuntimeID
		assessment Assessment
	}{
		{"unsupported expected runtime", "unknown", assessment},
		{"runtime mismatch", runtimematrix.RuntimePi, otherRuntime},
		{"fingerprint mismatch", runtimematrix.RuntimePi, otherSnapshot},
		{"zero assessment", runtimematrix.RuntimePi, Assessment{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateBinding(snapshot, test.runtime, test.assessment); err == nil {
				t.Fatal("ValidateBinding() accepted an invalid binding")
			}
		})
	}
}

func testSnapshot(t *testing.T) catalog.CatalogSnapshot {
	t.Helper()
	root := t.TempDir()
	families := make(map[string]string)
	for _, id := range catalog.ApprovedFamilyIDs() {
		manifestPath := "families/" + id + ".json"
		routerPath := "routers/" + id + ".md"
		families[id] = manifestPath
		writeTestJSON(t, root, manifestPath, map[string]any{
			"schemaVersion": 1,
			"id":            id,
			"router":        routerPath,
			"capabilities":  []string{},
			"agents":        []string{"test-runner"},
		})
		writeTestFile(t, root, routerPath, "# "+id+"\n")
	}
	writeTestJSON(t, root, "catalog.json", map[string]any{
		"schemaVersion": 1,
		"families":      families,
	})
	snapshot, err := catalog.BuildCatalogSnapshot(root, "catalog.json", catalog.AdmissionPolicy{})
	if err != nil {
		t.Fatalf("BuildCatalogSnapshot() error = %v", err)
	}
	return snapshot
}

func writeTestJSON(t *testing.T, root, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	writeTestFile(t, root, name, string(data))
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

// fakeAssessor proves the interface is implementable without claiming runtime support.
type fakeAssessor struct{}

func (fakeAssessor) RuntimeID() runtimematrix.RuntimeID { return "fake" }
func (fakeAssessor) Assess(catalog.CatalogSnapshot) (Assessment, error) {
	return Assessment{}, nil
}

var _ Assessor = fakeAssessor{}
