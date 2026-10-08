package runtimecompat_test

import (
	"os"
	"regexp"
	"testing"

	"github.com/refactor-ia/cortex/internal/runtimecompat"
	"github.com/refactor-ia/cortex/internal/runtimematrix"
)

// evidenceRow matches one runtime row of the docs/verification.md evidence table.
var evidenceRow = regexp.MustCompile(`(?m)^\| (Pi|OpenCode|Claude Code) \| ([0-9][0-9A-Za-z.\-+]*) \|`)

// publishedEvidenceVersions reads the certified versions the docs/verification.md
// publishes as merged real-runtime smoke evidence.
func publishedEvidenceVersions(t *testing.T) map[string]string {
	t.Helper()
	readme, err := os.ReadFile("../../docs/verification.md")
	if err != nil {
		t.Fatal(err)
	}
	rows := evidenceRow.FindAllStringSubmatch(string(readme), -1)
	published := make(map[string]string, len(rows))
	for _, row := range rows {
		if _, duplicate := published[row[1]]; duplicate {
			t.Fatalf("docs/verification.md evidence table lists %s more than once", row[1])
		}
		published[row[1]] = row[2]
	}
	return published
}

// TestBuiltInPolicyMatchesPublishedEvidence pins the certified versions to the
// docs/verification.md evidence table in both directions. Certifying a version
// docs/verification.md does not evidence, or publishing evidence the policy does not certify, fails
// here rather than silently shipping an unbacked certification claim.
func TestBuiltInPolicyMatchesPublishedEvidence(t *testing.T) {
	published := publishedEvidenceVersions(t)
	certified := map[string]string{
		"Pi":          runtimecompat.CertifiedPiVersion,
		"OpenCode":    runtimecompat.CertifiedOpenCodeVersion,
		"Claude Code": runtimecompat.CertifiedClaudeCodeVersion,
	}
	if len(published) != len(certified) {
		t.Fatalf("docs/verification.md evidence table lists %d runtimes, policy certifies %d", len(published), len(certified))
	}
	for runtime, want := range certified {
		got, ok := published[runtime]
		if !ok {
			t.Fatalf("policy certifies %s %s with no docs/verification.md evidence row", runtime, want)
		}
		if got != want {
			t.Errorf("%s: docs/verification.md evidences %s, policy certifies %s", runtime, got, want)
		}
	}
}

// TestBuiltInPolicyAdmitsExactlyTheCertifiedVersions confirms the certified
// versions resolve as compatible and that a neighbouring version does not.
func TestBuiltInPolicyAdmitsExactlyTheCertifiedVersions(t *testing.T) {
	got, err := runtimecompat.BuiltInPolicy().Evaluate(reports(t,
		detected(runtimecompat.CertifiedPiVersion),
		detected(runtimecompat.CertifiedOpenCodeVersion),
		claudeDetected(runtimecompat.CertifiedClaudeCodeVersion),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range got {
		if !observation.Present || observation.Compatibility != runtimematrix.Compatible {
			t.Fatalf("certified version was not admitted: %#v", observation)
		}
	}

	near, err := runtimecompat.BuiltInPolicy().Evaluate(reports(t,
		detected("0.85.2"), detected("1.18.26"), claudeDetected("2.1.252"),
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, observation := range near {
		if observation.Compatibility != runtimematrix.CompatibilityUnknown {
			t.Fatalf("exact-version admission leaked to a neighbouring version: %#v", observation)
		}
	}
}
